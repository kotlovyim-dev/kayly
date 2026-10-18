package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kotlovyim-dev/kayly/internal/store"
)

// fakeBotAPI is the Bot API of bots whose tokens are in tokens. It keeps each bot's webhook
// and menu button and records every call that changes one, plus events added by the test.
type fakeBotAPI struct {
	mu      sync.Mutex
	tokens  map[string]string // token → username
	webhook map[string]string // token → url
	secret  map[string]string // token → secret_token
	menu    map[string]string // token → "text url" of a web_app button
	failSet bool              // setWebhook and setChatMenuButton fail
	// hook runs before every call, outside the lock, with the method name.
	hook func(method string)
	log  []string
	srv  *httptest.Server
}

func newFakeBotAPI(t *testing.T) *fakeBotAPI {
	f := &fakeBotAPI{
		tokens:  map[string]string{"1:good": "app_bot"},
		webhook: map[string]string{}, secret: map[string]string{}, menu: map[string]string{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBotAPI) serve(w http.ResponseWriter, r *http.Request) {
	token, method, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
	var body struct {
		URL        string `json:"url"`
		Secret     string `json:"secret_token"`
		MenuButton struct {
			Text   string `json:"text"`
			WebApp struct {
				URL string `json:"url"`
			} `json:"web_app"`
		} `json:"menu_button"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook(method)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	answer := func(ok bool, desc string, result any) {
		json.NewEncoder(w).Encode(map[string]any{"ok": ok, "description": desc, "result": result})
	}
	if _, ok := f.tokens[token]; !ok {
		answer(false, "Unauthorized", nil)
		return
	}
	if f.failSet && strings.HasPrefix(method, "set") {
		answer(false, "Too Many Requests: retry after 5", nil)
		return
	}
	switch method {
	case "getMe":
		answer(true, "", map[string]string{"username": f.tokens[token]})
	case "getWebhookInfo":
		answer(true, "", map[string]string{"url": f.webhook[token]})
	case "getChatMenuButton":
		text, url, ok := strings.Cut(f.menu[token], " ")
		if !ok {
			answer(true, "", map[string]string{"type": "default"})
			return
		}
		answer(true, "", map[string]any{"type": "web_app", "text": text, "web_app": map[string]string{"url": url}})
	case "setWebhook":
		f.webhook[token], f.secret[token] = body.URL, body.Secret
		f.log = append(f.log, "setWebhook "+body.URL)
		answer(true, "", true)
	case "deleteWebhook":
		f.webhook[token] = ""
		f.log = append(f.log, "deleteWebhook")
		answer(true, "", true)
	case "setChatMenuButton":
		f.menu[token] = body.MenuButton.Text + " " + body.MenuButton.WebApp.URL
		f.log = append(f.log, "setChatMenuButton "+f.menu[token])
		answer(true, "", true)
	default:
		answer(false, "Not Found", nil)
	}
}

func (f *fakeBotAPI) event(e string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, e)
}

// take returns the log and clears it.
func (f *fakeBotAPI) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.log
	f.log = nil
	return out
}

func (f *fakeBotAPI) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// botApp has a web service, which the menu button opens, and a bot in mode.
func botApp(mode string) string {
	tg := "mode = \"polling\""
	if mode == "webhook" {
		tg = "mode = \"webhook\"\npath = \"/telegram\""
	}
	return `
[services.web]
port = 3000

[services.bot]
port = 8080

[services.bot.telegram]
token = "BOT_TOKEN"
` + tg + `
menu_button = { text = "Open", url = "${web.url}" }

[env]
BOT_TOKEN = {}
`
}

func newBotHarness(t *testing.T, mode string) *harness {
	h := newHarness(t)
	h.writeConfig(botApp(mode))
	h.setEnv("BOT_TOKEN", "1:good")
	return h
}

func (h *harness) setEnv(key, value string) {
	h.t.Helper()
	p, err := h.st.Project(context.Background(), "app")
	must(h.t, err)
	must(h.t, h.st.SetEnv(context.Background(), p.ID, key, value))
}

func TestWebhookBotIsSetOnceAndRepairedByReconcile(t *testing.T) {
	h := newBotHarness(t, "webhook")
	h.mustDeploy()
	want := []string{
		"setWebhook https://bot.app.1-2-3-4.sslip.io/telegram",
		"setChatMenuButton Open https://web.app.1-2-3-4.sslip.io",
	}
	if got := h.tg.take(); !slices.Equal(got, want) {
		t.Errorf("first deploy: %v, want %v", got, want)
	}
	// The bot checks updates against the secret Telegram was given.
	secret := h.rt.env(ContainerName("app", "bot", 1))["TELEGRAM_WEBHOOK_SECRET"]
	if len(secret) != 64 || h.tg.secret["1:good"] != secret {
		t.Errorf("secret delivered %q, given to Telegram %q", secret, h.tg.secret["1:good"])
	}
	if _, ok := h.rt.env(ContainerName("app", "web", 1))["TELEGRAM_WEBHOOK_SECRET"]; ok {
		t.Error("the webhook secret reached a service that is not a webhook bot")
	}

	// An ordinary deploy and a quiet reconcile leave Telegram alone.
	h.mustDeploy()
	must(t, h.d.Reconcile(context.Background()))
	if got := h.tg.take(); len(got) != 0 {
		t.Errorf("second deploy and reconcile changed Telegram: %v", got)
	}

	// Someone pointed the webhook elsewhere: the reconciler points it back.
	h.tg.set(func() { h.tg.webhook["1:good"] = "https://elsewhere.example.com/" })
	must(t, h.d.Reconcile(context.Background()))
	if got := h.tg.take(); !slices.Equal(got, want[:1]) {
		t.Errorf("reconcile: %v, want %v", got, want[:1])
	}
}

func TestBadBotTokenFailsBeforeAnythingLiveChanges(t *testing.T) {
	h := newBotHarness(t, "polling")
	h.mustDeploy()
	h.setEnv("BOT_TOKEN", "2:revoked")
	_, log, err := h.deploy()
	if err == nil || !strings.Contains(err.Error(), "the token in BOT_TOKEN does not work: getMe: Unauthorized") {
		t.Fatalf("err = %v\n%s", err, log)
	}
	if h.rt.wasRemoved(ContainerName("app", "bot", 1)) {
		t.Error("the live polling bot was stopped for a release with a bad token")
	}
	if slices.Contains(h.rt.names(), ContainerName("app", "bot", 2)) || strings.Contains(err.Error(), "revoked") {
		t.Errorf("r2 started or the token leaked: %v %v", h.rt.names(), err)
	}
}

func TestPollingBotDropsItsWebhookBeforeItStarts(t *testing.T) {
	h := newBotHarness(t, "webhook")
	h.mustDeploy()
	h.tg.take()

	h.writeConfig(botApp("polling"))
	h.rt.hook = func(method, arg string) error {
		if method == "Run" || method == "Remove" {
			h.tg.event(method + " " + arg)
		}
		return nil
	}
	h.mustDeploy()
	got := h.tg.take()
	del, stop, run := slices.Index(got, "deleteWebhook"), slices.Index(got, "Remove app_bot_r1"), slices.Index(got, "Run app_bot_r2")
	if del < 0 || del > stop || stop > run {
		t.Errorf("want deleteWebhook, then the old bot stopped, then the new one started: %v", got)
	}
	if h.tg.webhook["1:good"] != "" {
		t.Error("a polling bot still has a webhook")
	}
}

func TestFailedBotSetupLeavesReleaseLiveAndIsRetried(t *testing.T) {
	h := newBotHarness(t, "webhook")
	h.tg.set(func() { h.tg.failSet = true })
	rel, log, err := h.deploy()
	if err != nil || !strings.Contains(log, "warning: bot: setWebhook: Too Many Requests") {
		t.Fatalf("deploy: %v\n%s", err, log)
	}
	p, _ := h.st.Project(context.Background(), "app")
	if p.ActiveRelease != rel.ID {
		t.Fatal("a failed Telegram call after the switch rolled the release back")
	}
	must(t, h.d.Reconcile(context.Background()))
	must(t, h.d.Reconcile(context.Background()))
	if len(h.alerts) != 1 || !strings.Contains(h.alerts[0], "Telegram setup of bot failed") {
		t.Errorf("alerts = %q, want one", h.alerts)
	}

	h.tg.set(func() { h.tg.failSet = false })
	must(t, h.d.Reconcile(context.Background()))
	if h.tg.webhook["1:good"] != "https://bot.app.1-2-3-4.sslip.io/telegram" {
		t.Errorf("webhook = %q after the retry", h.tg.webhook["1:good"])
	}
	if len(h.alerts) != 2 || !strings.Contains(h.alerts[1], "works again") {
		t.Errorf("alerts = %q, want an all clear", h.alerts)
	}
}

// A daemon that dies at a Telegram step leaves state the next daemon converges from: before
// the start the previous release keeps serving; after the switch the new one does. Either
// way the webhook ends up set.
func TestCrashAtBotSteps(t *testing.T) {
	for _, tt := range []struct {
		method   string
		wantLive int
	}{
		{"getMe", 1},
		{"setWebhook", 2},
	} {
		t.Run(tt.method, func(t *testing.T) {
			h := newBotHarness(t, "webhook")
			// r1 uses only the default host; r2 adds a custom domain, so it sets the webhook.
			h.mustDeploy()
			p, _ := h.st.Project(context.Background(), "app")
			must(t, h.st.AddDomain(context.Background(), store.Domain{ProjectID: p.ID, Service: "bot", Hostname: "bot.example.com"}))

			reached, release := make(chan struct{}), make(chan struct{})
			var hit atomic.Bool
			h.tg.set(func() {
				h.tg.hook = func(method string) {
					if method == tt.method && hit.CompareAndSwap(false, true) {
						close(reached)
						<-release // only the zombie's call hangs
					}
				}
			})
			done := make(chan struct{})
			go func() { defer close(done); h.deploy() }()
			<-reached

			restarted := h.newDeployer()
			must(t, restarted.Recover(context.Background()))
			must(t, restarted.Reconcile(context.Background()))
			p, _ = h.st.Project(context.Background(), "app")
			rel, _ := h.st.ReleaseByID(context.Background(), p.ActiveRelease)
			if rel.Seq != tt.wantLive {
				t.Errorf("live = r%d, want r%d", rel.Seq, tt.wantLive)
			}
			if !slices.Contains(h.rt.names(), ContainerName("app", "bot", tt.wantLive)) {
				t.Errorf("containers = %v", h.rt.names())
			}
			// Whichever release is live, the webhook follows the current domains.
			if got, want := h.tg.webhook["1:good"], "https://bot.example.com/telegram"; got != want {
				t.Errorf("webhook = %q, want %q", got, want)
			}
			close(release)
			<-done
		})
	}
}
