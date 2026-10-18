package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/notify"
	"github.com/kotlovyim-dev/kayly/internal/offsite"
	"github.com/kotlovyim-dev/kayly/internal/proxy"
	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

type entry struct {
	name, body, link string
	typ              byte
}

func archive(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Size: int64(len(e.body)), Linkname: e.link}
		if e.typ == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	return &buf
}

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	err := extract(archive(t,
		entry{name: "kayly.toml", body: "[services.web]"},
		entry{name: "src/", typ: tar.TypeDir},
		entry{name: "src/index.js", body: "ok"},
		entry{name: "src/link.js", typ: tar.TypeSymlink, link: "index.js"},
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "src/link.js")); string(b) != "ok" {
		t.Errorf("symlink content = %q", b)
	}

	bad := map[string]*bytes.Buffer{
		"escape":           archive(t, entry{name: "../evil", body: "x"}),
		"absolute":         archive(t, entry{name: "/etc/evil", body: "x"}),
		"symlink outside":  archive(t, entry{name: "l", typ: tar.TypeSymlink, link: "../../etc/passwd"}),
		"symlink absolute": archive(t, entry{name: "l", typ: tar.TypeSymlink, link: "/etc/passwd"}),
		"write via link":   archive(t, entry{name: "l", typ: tar.TypeSymlink, link: "x"}, entry{name: "l", body: "y"}),
		"device":           archive(t, entry{name: "dev", typ: tar.TypeChar}),
		// d resolves to ".", so d/e → ../x looks local but resolves outside.
		"escape through a linked dir": archive(t,
			entry{name: "d", typ: tar.TypeSymlink, link: "."},
			entry{name: "d/e", typ: tar.TypeSymlink, link: "../x"},
			entry{name: "d/e/f", body: "pwned"}),
		"not gzip": bytes.NewBufferString("plain text"),
	}
	for name, a := range bad {
		t.Run(name, func(t *testing.T) {
			if err := extract(a, t.TempDir()); err == nil {
				t.Error("accepted")
			}
		})
	}
}

type recordingProxy struct {
	mu    sync.Mutex
	loads int
	last  string
}

func (p *recordingProxy) Load(_ context.Context, cfg []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loads++
	p.last = string(cfg)
	return nil
}

type env struct {
	t       *testing.T
	trusted http.Handler
	public  http.Handler
	px      *recordingProxy
}

func newEnv(t *testing.T) *env {
	t.Helper()
	box, _ := secret.NewBox(bytes.Repeat([]byte{3}, 32))
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kayly.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	px := &recordingProxy{}
	// The handlers under test never reach the runtime or the builder.
	d := deploy.New(st, nil, nil, px, deploy.Options{Proxy: proxy.Options{AdminListen: "unix//x.sock"}}, log)
	s := New(st, d, t.TempDir(), log)
	s.Notifier = notify.New(st, log)
	s.Offsite = offsite.New(st, t.TempDir(), nil, log)
	s.UI = fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html>ui")},
		"assets/app-1.js": {Data: []byte("js")},
	}
	return &env{t: t, trusted: s.Handler(true), public: s.Handler(false), px: px}
}

func (e *env) do(h http.Handler, method, path, body, token string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestAuth(t *testing.T) {
	e := newEnv(t)
	if code, body := e.do(e.public, "GET", "/api/v1/projects", "", ""); code != 401 || !strings.Contains(body, "kayly token create") {
		t.Fatalf("no token: %d %s", code, body)
	}
	if code, _ := e.do(e.public, "GET", "/api/v1/projects", "", "kayly_wrong"); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	code, body := e.do(e.trusted, "POST", "/api/v1/tokens", `{"name":"laptop"}`, "")
	var tok TokenView
	json.Unmarshal([]byte(body), &tok)
	if code != 201 || tok.Token == "" {
		t.Fatalf("create token: %d %s", code, body)
	}
	if code, _ := e.do(e.public, "GET", "/api/v1/projects", "", tok.Token); code != 200 {
		t.Fatalf("valid token: %d", code)
	}
	// Tokens cannot be minted through the public listener, even with a token.
	if code, _ := e.do(e.public, "POST", "/api/v1/tokens", `{"name":"x"}`, tok.Token); code != 404 && code != 405 {
		t.Fatalf("public token creation: %d", code)
	}
}

func TestProjectsEnvDomains(t *testing.T) {
	e := newEnv(t)
	h := e.trusted
	check := func(method, path, body string, want int) string {
		t.Helper()
		code, out := e.do(h, method, path, body, "")
		if code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, out)
		}
		return out
	}
	check("POST", "/api/v1/projects", `{"name":"leryo"}`, 201)
	check("POST", "/api/v1/projects", `{"name":"leryo"}`, 409)
	check("POST", "/api/v1/projects", `{"name":"Bad_Name"}`, 400)
	check("POST", "/api/v1/projects", `{"name":"x","extra":1}`, 400)
	check("GET", "/api/v1/projects/nope/env", "", 404)

	check("PUT", "/api/v1/projects/leryo/env/SMTP_URL", `{"value":"smtp://secret@relay"}`, 204)
	check("PUT", "/api/v1/projects/leryo/env/bad-key", `{"value":"x"}`, 400)
	if out := check("GET", "/api/v1/projects/leryo/env", "", 200); strings.Contains(out, "secret@relay") || !strings.Contains(out, "SMTP_URL") {
		t.Fatalf("env list = %s", out)
	}
	check("DELETE", "/api/v1/projects/leryo/env/SMTP_URL", "", 204)

	check("POST", "/api/v1/projects/leryo/domains", `{"hostname":"Leryo.Example.com.","service":"app"}`, 201)
	if e.px.loads != 1 {
		t.Errorf("proxy loads after adding a domain = %d", e.px.loads)
	}
	check("POST", "/api/v1/projects/leryo/domains", `{"hostname":"leryo.example.com","service":"app"}`, 409)
	check("POST", "/api/v1/projects/leryo/domains", `{"hostname":"not a host","service":"app"}`, 400)
	if out := check("GET", "/api/v1/projects/leryo/domains", "", 200); !strings.Contains(out, `"leryo.example.com"`) {
		t.Fatalf("domains = %s", out)
	}
	check("DELETE", "/api/v1/projects/leryo/domains/leryo.example.com", "", 204)
	check("DELETE", "/api/v1/projects/leryo/domains/leryo.example.com", "", 404)
}

func TestDeployRejectsBadArchive(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")
	code, body := e.do(e.trusted, "POST", "/api/v1/projects/app/deploys", "not an archive", "")
	if code != 400 || !strings.Contains(body, "bad_source") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestSessionAndUI(t *testing.T) {
	e := newEnv(t)
	_, body := e.do(e.trusted, "POST", "/api/v1/tokens", `{"name":"browser"}`, "")
	var tok TokenView
	json.Unmarshal([]byte(body), &tok)

	// The UI and its assets need no session; unknown paths get the app.
	for path, want := range map[string]string{"/": "ui", "/projects/x": "ui", "/assets/app-1.js": "js"} {
		if code, out := e.do(e.public, "GET", path, "", ""); code != 200 || !strings.Contains(out, want) {
			t.Errorf("GET %s = %d %q", path, code, out)
		}
	}

	if code, _ := e.do(e.public, "POST", "/api/v1/session", `{"token":"kayly_bad"}`, ""); code != 401 {
		t.Fatalf("login with a bad token: %d", code)
	}
	rec := httptest.NewRecorder()
	e.public.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/session", strings.NewReader(`{"token":"`+tok.Token+`"}`)))
	cookies := rec.Result().Cookies()
	if rec.Code != 204 || len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: %d %+v", rec.Code, cookies)
	}

	call := func(method, path string, csrf bool) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"name":"x"}`))
		req.AddCookie(cookies[0])
		if csrf {
			req.Header.Set(csrfHeader, "1")
		}
		rec := httptest.NewRecorder()
		e.public.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := call("GET", "/api/v1/projects", false); code != 200 {
		t.Errorf("GET with session: %d", code)
	}
	if code := call("POST", "/api/v1/projects", false); code != 403 {
		t.Errorf("POST with session but no CSRF header: %d", code)
	}
	if code := call("POST", "/api/v1/projects", true); code != 201 {
		t.Errorf("POST with session and CSRF header: %d", code)
	}
}

func TestNotificationsNeverEchoSecrets(t *testing.T) {
	e := newEnv(t)
	put := func(body string) (int, string) { return e.do(e.trusted, "PUT", "/api/v1/notifications", body, "") }
	if code, out := put(`{"telegram_token":"123:secret"}`); code != 400 {
		t.Fatalf("token without chat: %d %s", code, out)
	}
	if code, out := put(`{"telegram_token":"123:secret","telegram_chat":"42","webhook_url":"https://h.example/k3y"}`); code != 204 {
		t.Fatalf("put: %d %s", code, out)
	}
	// A later change keeps the fields it does not name.
	if code, out := put(`{"webhook_url":""}`); code != 204 {
		t.Fatalf("clear webhook: %d %s", code, out)
	}
	code, out := e.do(e.trusted, "GET", "/api/v1/notifications", "", "")
	if code != 200 || strings.Contains(out, "secret") || strings.Contains(out, "k3y") ||
		!strings.Contains(out, `"telegram":true`) || !strings.Contains(out, `"webhook":false`) {
		t.Fatalf("get: %d %s", code, out)
	}
}

func TestOffsiteSetupValidatesAndHidesSecrets(t *testing.T) {
	e := newEnv(t)
	if code, out := e.do(e.trusted, "PUT", "/api/v1/offsite", `{"endpoint":"x"}`, ""); code != 400 {
		t.Fatalf("incomplete setup: %d %s", code, out)
	}
	if code, out := e.do(e.trusted, "GET", "/api/v1/offsite", "", ""); code != 200 || !strings.Contains(out, `"configured":false`) {
		t.Fatalf("status: %d %s", code, out)
	}
	if code, _ := e.do(e.trusted, "POST", "/api/v1/offsite/sync", "", ""); code != 204 {
		t.Fatalf("sync without a bucket should be a no-op, got %d", code)
	}
}

func TestRestoreNeedsTheProjectNameAsConfirmation(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")
	for _, body := range []string{
		`{"name":"20260101T000000Z-daily.dump"}`,
		`{"name":"20260101T000000Z-daily.dump","confirm":"other"}`,
	} {
		code, out := e.do(e.trusted, "POST", "/api/v1/projects/app/backups/restore", body, "")
		if code != 400 || !strings.Contains(out, "confirm_required") {
			t.Fatalf("%s: %d %s", body, code, out)
		}
	}
}

func TestCommitFromTheClientIsSanitized(t *testing.T) {
	get := func(sha, msg string) store.Commit {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("X-Kayly-Commit", sha)
		r.Header.Set("X-Kayly-Commit-Message", msg)
		return commitFrom(r)
	}
	if c := get("3a77de6", "feat%3A%20%D0%BF%D1%80%D0%B8%D0%B2%D1%96%D1%82"); c != (store.Commit{SHA: "3a77de6", Message: "feat: привіт"}) {
		t.Errorf("plain = %+v", c)
	}
	if c := get("3a77de6-dirty", "one%0Atwo"); c.SHA != "3a77de6-dirty" || c.Message != "one" {
		t.Errorf("multi-line = %+v", c)
	}
	if c := get("3a77de6", strings.Repeat("x", 500)); len([]rune(c.Message)) != 200 {
		t.Errorf("long message = %d runes", len([]rune(c.Message)))
	}
	for _, bad := range []string{"", "xyz", "3a77de6; rm -rf", "<script>", "ABCDEF1"} {
		if c := get(bad, "msg"); c != (store.Commit{}) {
			t.Errorf("sha %q accepted: %+v", bad, c)
		}
	}
}

func TestReleaseLogRoute(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")
	if code, _ := e.do(e.trusted, "GET", "/api/v1/projects/app/releases/abc/log", "", ""); code != 400 {
		t.Errorf("non-numeric release: %d", code)
	}
	if code, _ := e.do(e.trusted, "GET", "/api/v1/projects/app/releases/r1/log", "", ""); code != 404 {
		t.Errorf("missing release: %d", code)
	}
}

func TestServicesAndServerRoutes(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")
	// A project that never deployed has no services, and that is an answer, not an error.
	if code, out := e.do(e.trusted, "GET", "/api/v1/projects/app/services", "", ""); code != 200 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("services: %d %s", code, out)
	}
	if code, _ := e.do(e.trusted, "GET", "/api/v1/projects/nope/services", "", ""); code != 404 {
		t.Fatalf("unknown project: %d", code)
	}
}

func TestTokenScopeAndProject(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"app", "other"} {
		e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"`+name+`"}`, "")
	}
	mint := func(body string) string {
		t.Helper()
		code, out := e.do(e.trusted, "POST", "/api/v1/tokens", body, "")
		var tv TokenView
		json.Unmarshal([]byte(out), &tv)
		if code != 201 {
			t.Fatalf("create token %s: %d %s", body, code, out)
		}
		return tv.Token
	}
	reader := mint(`{"name":"reader","scope":"read"}`)
	boundAdmin := mint(`{"name":"boss","scope":"admin","project":"app"}`)
	// A bound token cannot restore another project's backup into its own.
	if code, out := e.do(e.public, "POST", "/api/v1/projects/app/backups/restore",
		`{"name":"latest","from_project":"other","confirm":"app"}`, boundAdmin); code != 403 {
		t.Errorf("restore from another project = %d %s", code, out)
	}
	agent := mint(`{"name":"agent","scope":"deploy","project":"app","expires_days":7}`)

	for _, c := range []struct {
		token, method, path string
		want                int
	}{
		{reader, "GET", "/api/v1/projects/app", 200},
		{reader, "PUT", "/api/v1/projects/app/env/KEY", 403},
		{agent, "PUT", "/api/v1/projects/app/env/KEY", 400}, // allowed; fails on a missing body, not on auth
		{agent, "GET", "/api/v1/projects/other", 403},
		{agent, "POST", "/api/v1/projects/app/addons", 403},
		{agent, "POST", "/api/v1/projects", 403},
		{agent, "GET", "/api/v1/notifications", 403},
		{agent, "GET", "/api/v1/projects", 200},
	} {
		if code, out := e.do(e.public, c.method, c.path, "", c.token); code != c.want {
			t.Errorf("%s %s = %d, want %d (%s)", c.method, c.path, code, c.want, out)
		}
	}

	_, out := e.do(e.public, "GET", "/api/v1/projects", "", agent)
	if strings.Contains(out, "other") || !strings.Contains(out, "app") {
		t.Errorf("bound token sees %s", out)
	}

	// Refused calls are not changes; allowed writes are, under the token's name.
	_, out = e.do(e.trusted, "GET", "/api/v1/audit", "", "")
	if !strings.Contains(out, `"token":"agent"`) || strings.Contains(out, "addons") {
		t.Errorf("audit log = %s", out)
	}
}
