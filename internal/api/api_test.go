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

	"github.com/kotlovyim-dev/kayly/internal/deploy"
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
