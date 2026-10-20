package upgrade

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// release serves one signed release, v1.1.0, and says it is the latest.
type release struct {
	files map[string][]byte
	key   ed25519.PublicKey
}

func newRelease(t *testing.T, bins map[string][]byte) *release {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var sums bytes.Buffer
	files := map[string][]byte{}
	for name, b := range bins {
		h := sha256.Sum256(b)
		sums.WriteString(hex.EncodeToString(h[:]) + "  " + name + "\n")
		files[name] = b
	}
	files["checksums.txt"] = sums.Bytes()
	files["checksums.txt.sig"] = ed25519.Sign(priv, sums.Bytes())
	return &release{files: files, key: pub}
}

func (r *release) source(t *testing.T) Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/latest" {
			w.Write([]byte(`{"tag_name":"v1.1.0","name":"Kayly 1.1"}`))
			return
		}
		b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/download/v1.1.0/")]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return Source{Download: srv.URL + "/download", Latest: srv.URL + "/latest", Key: r.key, Client: srv.Client()}
}

func TestFetchVerifiesSignatureAndChecksum(t *testing.T) {
	ctx := context.Background()
	bin := []byte("\x7fELF the new kayly")
	rel := newRelease(t, map[string][]byte{"kayly-linux-amd64": bin, "kayly-linux-arm64": []byte("arm")})
	src := rel.source(t)

	v, err := src.LatestVersion(ctx)
	if err != nil || v != "v1.1.0" {
		t.Fatalf("latest = %q, %v", v, err)
	}
	got, err := src.Fetch(ctx, v, "amd64")
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("fetch = %q, %v", got, err)
	}

	bad := map[string]func(){
		"a binary that does not match its checksum": func() { rel.files["kayly-linux-amd64"] = []byte("tampered") },
		"checksums changed after signing": func() {
			rel.files["checksums.txt"] = append(bytes.Clone(rel.files["checksums.txt"]), "deadbeef  kayly-linux-riscv\n"...)
		},
		"a signature by another key": func() {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			rel.files["checksums.txt.sig"] = ed25519.Sign(other, rel.files["checksums.txt"])
		},
		"no signature": func() { delete(rel.files, "checksums.txt.sig") },
	}
	for name, spoil := range bad {
		t.Run(name, func(t *testing.T) {
			saved := map[string][]byte{}
			for k, v := range rel.files {
				saved[k] = v
			}
			t.Cleanup(func() { rel.files = saved })
			spoil()
			if _, err := src.Fetch(ctx, "v1.1.0", "amd64"); err == nil {
				t.Fatal("fetch accepted it")
			}
		})
	}
	if _, err := src.Fetch(ctx, "v1.1.0", "mips"); err == nil || !strings.Contains(err.Error(), "no checksum for kayly-linux-mips") {
		t.Fatalf("unknown arch: %v", err)
	}
}

func TestInstallReplacesAndKeepsThePrevious(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kayly")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install([]byte("new"), path); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{path: "new", path + ".previous": "old"} {
		if b, _ := os.ReadFile(file); string(b) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(file), b, want)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 2 {
		t.Errorf("left %d files behind, want the binary and its previous", len(entries))
	}
	if err := Install([]byte("x"), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("installed over a binary that does not exist")
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v0.5.0", "v1.0.0", -1},
		{"v1.10.0", "v1.9.3", 1},
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", 1},
	} {
		if got, err := Compare(c.a, c.b); err != nil || got != c.want {
			t.Errorf("Compare(%s, %s) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
	for _, v := range []string{"dev", "1.0.0", "v1.0", "v1.x.0"} {
		if _, err := Compare(v, "v1.0.0"); err == nil || Valid(v) == nil {
			t.Errorf("%q parsed as a version", v)
		}
	}
}

// The installer checks releases with openssl and the same key.
func TestInstallScriptCarriesTheReleaseKey(t *testing.T) {
	script, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(script, bytes.TrimSpace(releaseKey)) {
		t.Fatal("install.sh does not carry internal/upgrade/release.pub")
	}
	if _, err := GitHub(); err != nil {
		t.Fatal(err)
	}
}
