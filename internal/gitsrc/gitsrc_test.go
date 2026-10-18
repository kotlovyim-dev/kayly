package gitsrc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyRoundTrip(t *testing.T) {
	priv, pub, err := GenerateKey("kayly:app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, " kayly:app") {
		t.Errorf("public key = %q", pub)
	}
	again, err := PublicKey(priv)
	if err != nil || !strings.HasPrefix(pub, again) {
		t.Errorf("PublicKey = %q, %v; want a prefix of %q", again, err, pub)
	}
}

func TestValidURLAndRef(t *testing.T) {
	for _, u := range []string{"git@github.com:me/app.git", "ssh://git@example.com:2222/me/app.git"} {
		if err := ValidURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"https://github.com/me/app.git", "/srv/repo", "file:///srv/repo", "-oProxyCommand=x@h:p",
		"ext::sh -c id", "git@host:../../etc", "git@-host:x", ""} {
		if ValidURL(u) == nil {
			t.Errorf("%q accepted", u)
		}
	}
	for _, r := range []string{"main", "release/1.2", "v1.0.0", "0123abcd"} {
		if err := ValidRef(r); err != nil {
			t.Errorf("%s: %v", r, err)
		}
	}
	for _, r := range []string{"", "-f", "--upload-pack=x", "a..b", "a b"} {
		if ValidRef(r) == nil {
			t.Errorf("%q accepted", r)
		}
	}
}

func TestFetchTakesAShallowCopyOfARef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "kayly.toml"), []byte("[services.web]\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "first\n\nbody")
	os.WriteFile(filepath.Join(repo, "kayly.toml"), []byte("[services.api]\n"), 0o644)
	run("commit", "-q", "-am", "second")

	dir := t.TempDir()
	c, err := fetch(context.Background(), repo, "main", dir, []string{"GIT_ALLOW_PROTOCOL=file"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Message != "second" || len(c.SHA) != 40 {
		t.Errorf("commit = %+v", c)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "kayly.toml")); string(b) != "[services.api]\n" {
		t.Errorf("kayly.toml = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error(".git left in the build context")
	}

	if _, err := fetch(context.Background(), repo, "no-such-branch", t.TempDir(), []string{"GIT_ALLOW_PROTOCOL=file"}); err == nil {
		t.Error("fetching a missing ref succeeded")
	}
}
