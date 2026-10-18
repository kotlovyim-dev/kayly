// Package telegram calls the few Bot API methods Kayly needs to check a bot's token and to
// point its webhook and menu button where kayly.toml says (ARCHITECTURE.md §6).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// API is the origin of the Bot API.
const API = "https://api.telegram.org"

var client = &http.Client{
	Timeout: 15 * time.Second,
	// The token is in the path; a redirect would carry it to another host.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Bot is one bot. Its errors never contain the token.
type Bot struct {
	// API is the Bot API origin, API unless a test replaces it.
	API   string
	Token string
}

// MenuButton is a bot's web_app menu button. The zero value stands for any other kind.
type MenuButton struct {
	Text string
	URL  string
}

// Username checks the token with getMe and returns the bot's username.
func (b Bot) Username(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	err := b.call(ctx, "getMe", nil, &me)
	return me.Username, err
}

// WebhookURL returns the URL Telegram posts updates to; empty when the bot polls.
func (b Bot) WebhookURL(ctx context.Context) (string, error) {
	var info struct {
		URL string `json:"url"`
	}
	err := b.call(ctx, "getWebhookInfo", nil, &info)
	return info.URL, err
}

// SetWebhook makes Telegram post updates to url with secret in the
// X-Telegram-Bot-Api-Secret-Token header.
func (b Bot) SetWebhook(ctx context.Context, url, secret string) error {
	return b.call(ctx, "setWebhook", map[string]string{"url": url, "secret_token": secret}, nil)
}

// DeleteWebhook switches the bot to polling. Pending updates are kept for getUpdates.
func (b Bot) DeleteWebhook(ctx context.Context) error {
	return b.call(ctx, "deleteWebhook", map[string]bool{"drop_pending_updates": false}, nil)
}

// MenuButton returns the bot's default menu button.
func (b Bot) MenuButton(ctx context.Context) (MenuButton, error) {
	var m struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		WebApp struct {
			URL string `json:"url"`
		} `json:"web_app"`
	}
	if err := b.call(ctx, "getChatMenuButton", nil, &m); err != nil || m.Type != "web_app" {
		return MenuButton{}, err
	}
	return MenuButton{Text: m.Text, URL: m.WebApp.URL}, nil
}

// SetMenuButton makes the bot's default menu button open m.URL as a Mini App.
func (b Bot) SetMenuButton(ctx context.Context, m MenuButton) error {
	button := map[string]any{"type": "web_app", "text": m.Text, "web_app": map[string]string{"url": m.URL}}
	return b.call(ctx, "setChatMenuButton", map[string]any{"menu_button": button}, nil)
}

func (b Bot) call(ctx context.Context, method string, params, result any) error {
	if params == nil {
		params = struct{}{}
	}
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.API+"/bot"+b.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: invalid bot token", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	if !r.OK {
		return fmt.Errorf("%s: %s", method, r.Description)
	}
	if result != nil {
		if err := json.Unmarshal(r.Result, result); err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
	}
	return nil
}
