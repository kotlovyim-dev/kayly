package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceKeysAreMadeOnceAndSecretShownOnce(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")

	if code, _ := e.do(e.trusted, "PUT", "/api/v1/projects/app/source", `{"url":"https://github.com/me/app.git"}`, ""); code != 400 {
		t.Fatalf("https URL accepted: %d", code)
	}
	if code, _ := e.do(e.trusted, "GET", "/api/v1/projects/app/source", "", ""); code != 404 {
		t.Fatalf("source before set: %d", code)
	}

	var first, second, shown SourceView
	code, body := e.do(e.trusted, "PUT", "/api/v1/projects/app/source", `{"url":"git@github.com:me/app.git"}`, "")
	json.Unmarshal([]byte(body), &first)
	if code != 200 || first.Branch != "main" || !strings.HasPrefix(first.PublicKey, "ssh-ed25519 ") || first.WebhookSecret == "" {
		t.Fatalf("set: %d %s", code, body)
	}
	_, body = e.do(e.trusted, "PUT", "/api/v1/projects/app/source", `{"url":"git@github.com:me/app.git","branch":"prod"}`, "")
	json.Unmarshal([]byte(body), &second)
	if second.PublicKey != first.PublicKey || second.Branch != "prod" || second.WebhookSecret != "" {
		t.Errorf("changing the branch must keep the key and not reveal the secret: %+v", second)
	}
	_, body = e.do(e.trusted, "GET", "/api/v1/projects/app/source", "", "")
	json.Unmarshal([]byte(body), &shown)
	if shown.WebhookSecret != "" || strings.Contains(body, first.WebhookSecret) {
		t.Errorf("GET leaks the webhook secret: %s", body)
	}
}

func TestGitHubWebhook(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")
	var v SourceView
	_, body := e.do(e.trusted, "PUT", "/api/v1/projects/app/source", `{"url":"git@github.com:me/app.git","branch":"main"}`, "")
	json.Unmarshal([]byte(body), &v)

	post := func(project, event, payload, secret string) int {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(payload))
		req := httptest.NewRequest("POST", "/api/v1/hooks/github/"+project, strings.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Event", event)
		rec := httptest.NewRecorder()
		e.public.ServeHTTP(rec, req)
		return rec.Code
	}
	sha := strings.Repeat("a", 40)
	push := `{"ref":"refs/heads/main","after":"` + sha + `"}`
	for _, c := range []struct {
		name, project, event, payload, secret string
		want                                  int
	}{
		{"wrong secret", "app", "push", push, "nope", 401},
		{"unknown project", "ghost", "push", push, v.WebhookSecret, 401},
		{"ping", "app", "ping", `{}`, v.WebhookSecret, 204},
		{"another branch", "app", "push", `{"ref":"refs/heads/dev","after":"` + sha + `"}`, v.WebhookSecret, 204},
		{"branch deleted", "app", "push", `{"ref":"refs/heads/main","after":"` + strings.Repeat("0", 40) + `","deleted":true}`, v.WebhookSecret, 204},
		{"no commit", "app", "push", `{"ref":"refs/heads/main","after":"-x"}`, v.WebhookSecret, 400},
		{"push to the followed branch", "app", "push", push, v.WebhookSecret, 202},
	} {
		if got := post(c.project, c.event, c.payload, c.secret); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}

	// A bad signature is not a request header the proxy can fake: no token or cookie helps.
	req := httptest.NewRequest("POST", "/api/v1/hooks/github/app", strings.NewReader(push))
	req.Header.Set("Authorization", "Bearer whatever")
	rec := httptest.NewRecorder()
	e.public.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unsigned push = %d", rec.Code)
	}
}
