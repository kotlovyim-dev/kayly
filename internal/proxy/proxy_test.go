package proxy

import (
	"bytes"
	"context"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

var opts = Options{AdminListen: "unix//run/kayly/caddy.sock", Email: "ops@example.com"}

func TestConfigGolden(t *testing.T) {
	routes := []Route{
		{Host: "ten00.example.com", Upstream: "ten00-web-r4:3000"},
		{Host: "leryo.example.com", Upstream: "leryo-app-r12:3000"},
	}
	got, err := Config(routes, opts)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "two-projects.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("config differs from %s (run with -update after checking the diff):\n%s", golden, got)
	}

	// Route order must not change the output, or every reconcile would reload Caddy.
	routes[0], routes[1] = routes[1], routes[0]
	again, _ := Config(routes, opts)
	if !bytes.Equal(got, again) {
		t.Error("config depends on route order")
	}
}

func TestConfigErrors(t *testing.T) {
	tests := []struct {
		name   string
		routes []Route
		opt    Options
		want   string
	}{
		{"no admin", nil, Options{}, "admin listen"},
		{"duplicate host", []Route{{"a.io", "x:1"}, {"a.io", "y:1"}}, opts, "routed twice"},
		{"no port", []Route{{"a.io", "x"}}, opts, "upstream"},
		{"empty", []Route{{"", "x:1"}}, opts, "required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Config(tt.routes, tt.opt); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestClientLoad(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "caddy.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/load" || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if bytes.Contains(got, []byte("broken")) {
			http.Error(w, `{"error":"loading new config: broken"}`, http.StatusBadRequest)
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	c := NewClient(sock)
	if err := c.Load(context.Background(), []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Errorf("body = %s", got)
	}
	err = c.Load(context.Background(), []byte(`"broken"`))
	if err == nil || !strings.Contains(err.Error(), "loading new config: broken") {
		t.Fatalf("err = %v, want Caddy's message", err)
	}
}
