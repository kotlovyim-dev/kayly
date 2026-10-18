package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAPI answers like the Bot API for token "1:good" and refuses any other.
func fakeAPI(t *testing.T, calls *[]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*calls = append(*calls, r.URL.Path+" "+string(b))
		if !strings.HasPrefix(r.URL.Path, "/bot1:good/") {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Unauthorized"})
			return
		}
		results := map[string]any{
			"getMe":             map[string]any{"username": "kayly_bot"},
			"getWebhookInfo":    map[string]any{"url": "https://bot.example.com/telegram", "pending_update_count": 0},
			"getChatMenuButton": map[string]any{"type": "web_app", "text": "Open", "web_app": map[string]string{"url": "https://app.example.com"}},
		}
		res, ok := results[strings.TrimPrefix(r.URL.Path, "/bot1:good/")]
		if !ok {
			res = true
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBot(t *testing.T) {
	var calls []string
	srv := fakeAPI(t, &calls)
	b := Bot{API: srv.URL, Token: "1:good"}
	ctx := context.Background()

	if name, err := b.Username(ctx); err != nil || name != "kayly_bot" {
		t.Errorf("Username = %q, %v", name, err)
	}
	if u, err := b.WebhookURL(ctx); err != nil || u != "https://bot.example.com/telegram" {
		t.Errorf("WebhookURL = %q, %v", u, err)
	}
	if m, err := b.MenuButton(ctx); err != nil || m != (MenuButton{"Open", "https://app.example.com"}) {
		t.Errorf("MenuButton = %+v, %v", m, err)
	}
	calls = nil
	for _, err := range []error{
		b.SetWebhook(ctx, "https://bot.example.com/telegram", "s3cret"),
		b.DeleteWebhook(ctx),
		b.SetMenuButton(ctx, MenuButton{"Open", "https://app.example.com"}),
	} {
		if err != nil {
			t.Error(err)
		}
	}
	want := []string{
		`/bot1:good/setWebhook {"secret_token":"s3cret","url":"https://bot.example.com/telegram"}`,
		`/bot1:good/deleteWebhook {"drop_pending_updates":false}`,
		`/bot1:good/setChatMenuButton {"menu_button":{"text":"Open","type":"web_app","web_app":{"url":"https://app.example.com"}}}`,
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestErrorsNeverCarryTheToken(t *testing.T) {
	var calls []string
	srv := fakeAPI(t, &calls)
	ctx := context.Background()
	for _, b := range []Bot{
		{API: srv.URL, Token: "2:revoked"},
		{API: "http://127.0.0.1:1", Token: "2:revoked"}, // nothing listens
		{API: srv.URL, Token: "2:bad\x7f"},
	} {
		_, err := b.Username(ctx)
		if err == nil || strings.Contains(err.Error(), "revoked") {
			t.Errorf("err = %v", err)
		}
	}
	if _, err := (Bot{API: srv.URL, Token: "2:revoked"}).Username(ctx); err.Error() != "getMe: Unauthorized" {
		t.Errorf("err = %v, want Telegram's description", err)
	}
}
