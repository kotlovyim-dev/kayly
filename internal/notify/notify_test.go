package notify

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) Setting(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}

func (s *memSettings) SetSetting(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = v
	return nil
}

func newNotifier(t *testing.T, tg string) *Notifier {
	n := New(&memSettings{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n.telegramAPI = tg
	return n
}

func TestSendReachesBothTargets(t *testing.T) {
	var got []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.URL.Path+" "+string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	n := newNotifier(t, srv.URL)
	ctx := context.Background()
	if err := n.SetTargets(ctx, Targets{TelegramToken: "123:abc", TelegramChat: "42", WebhookURL: srv.URL + "/hook"}); err != nil {
		t.Fatal(err)
	}
	if err := n.Send(ctx, "backup failed"); err != nil {
		t.Fatal(err)
	}
	want := []string{`/bot123:abc/sendMessage {"chat_id":"42","text":"backup failed"}`, `/hook {"text":"backup failed"}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFailureNeverLeaksTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	n := newNotifier(t, srv.URL)
	ctx := context.Background()
	n.SetTargets(ctx, Targets{TelegramToken: "123:secret", TelegramChat: "42"})
	err := n.Send(ctx, "x")
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
	srv.Close()
	if err := n.Send(ctx, "x"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unreachable err = %v", err)
	}
}

func TestSetTargetsValidates(t *testing.T) {
	n := newNotifier(t, "")
	for _, tc := range []Targets{{TelegramToken: "t"}, {TelegramChat: "c"}, {WebhookURL: "ftp://x"}, {WebhookURL: "nonsense"}} {
		if err := n.SetTargets(context.Background(), tc); err == nil {
			t.Errorf("%+v accepted", tc)
		}
	}
}

func TestNoTargetsIsNoop(t *testing.T) {
	if err := newNotifier(t, "").Send(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookRedirectIsNotFollowed(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer target.Close()
	redir := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusTemporaryRedirect))
	defer redir.Close()
	n := newNotifier(t, "")
	ctx := context.Background()
	n.SetTargets(ctx, Targets{WebhookURL: redir.URL})
	if err := n.Send(ctx, "x"); err == nil || hit {
		t.Fatalf("redirect followed (hit=%v, err=%v)", hit, err)
	}
}
