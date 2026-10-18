// Package notify tells the owner when something needs attention (ARCHITECTURE.md §9): a
// Telegram chat and/or a webhook. Targets live in the store, so a change applies at once.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const settingKey = "notifications"

// Targets are where messages go. Empty fields are disabled.
type Targets struct {
	TelegramToken string `json:"telegram_token,omitempty"`
	TelegramChat  string `json:"telegram_chat,omitempty"`
	WebhookURL    string `json:"webhook_url,omitempty"`
}

// Settings is the part of the store a Notifier needs.
type Settings interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Notifier sends messages to the configured targets.
type Notifier struct {
	settings Settings
	client   *http.Client
	log      *slog.Logger

	// telegramAPI is the Bot API origin. Replaced in tests.
	telegramAPI string
}

// New returns a Notifier.
func New(s Settings, log *slog.Logger) *Notifier {
	return &Notifier{
		settings: s, log: log,
		// A redirect would send the message to a host the URL check never saw.
		// ponytail: private addresses are allowed (a LAN relay is a normal webhook for a
		// one-owner server); restrict the route to admin tokens when scopes land (v0.3).
		client: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		telegramAPI: "https://api.telegram.org",
	}
}

// Targets returns the configured targets.
func (n *Notifier) Targets(ctx context.Context) (Targets, error) {
	raw, err := n.settings.Setting(ctx, settingKey)
	if err != nil || raw == "" {
		return Targets{}, err
	}
	var t Targets
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return Targets{}, fmt.Errorf("notify: stored targets: %w", err)
	}
	return t, nil
}

// SetTargets replaces the targets. A Telegram token needs a chat id, and a webhook must be
// an http(s) URL.
func (n *Notifier) SetTargets(ctx context.Context, t Targets) error {
	if (t.TelegramToken == "") != (t.TelegramChat == "") {
		return errors.New("telegram needs both a bot token and a chat id")
	}
	if t.WebhookURL != "" {
		if u, err := url.Parse(t.WebhookURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("webhook must be an http(s) URL")
		}
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if t == (Targets{}) {
		raw = nil
	}
	return n.settings.SetSetting(ctx, settingKey, string(raw))
}

// Notify sends text to every target. It never fails the caller: a notification that cannot
// be delivered is logged, because the event it reports has already been handled.
func (n *Notifier) Notify(ctx context.Context, text string) {
	if err := n.Send(ctx, text); err != nil {
		n.log.Error("notify", "err", err)
	}
}

// Send sends text to every target and returns the errors of those that failed.
func (n *Notifier) Send(ctx context.Context, text string) error {
	t, err := n.Targets(ctx)
	if err != nil {
		return err
	}
	var errs []error
	if t.TelegramToken != "" {
		errs = append(errs, n.telegram(ctx, t, text))
	}
	if t.WebhookURL != "" {
		errs = append(errs, n.webhook(ctx, t, text))
	}
	return errors.Join(errs...)
}

func (n *Notifier) telegram(ctx context.Context, t Targets, text string) error {
	body, _ := json.Marshal(map[string]string{"chat_id": t.TelegramChat, "text": text})
	if err := n.post(ctx, n.telegramAPI+"/bot"+t.TelegramToken+"/sendMessage", body); err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	return nil
}

func (n *Notifier) webhook(ctx context.Context, t Targets, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	if err := n.post(ctx, t.WebhookURL, body); err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	return nil
}

// post sends a JSON body. Its errors never contain target: a Telegram URL carries the bot token.
func (n *Notifier) post(ctx context.Context, target string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid URL")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
