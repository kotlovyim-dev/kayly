package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ten00 is not a git work tree; its .gitignore must still apply.
func TestSourceFilesWithoutGit(t *testing.T) {
	dir := tree(t, map[string]string{
		".gitignore":               "# build output\ndist\n.env*\n*.key\n/graphify-out\nlogs/\n",
		"package.json":             "{}",
		"src/main.ts":              "",
		"dist/index.html":          "",
		".env.local":               "",
		"admin.key":                "",
		"graphify-out/x.json":      "",
		"src/graphify-out/k":       "", // anchored pattern: only the root one is ignored
		"logs/today":               "",
		"src/logs":                 "", // "logs/" matches directories only
		"node_modules/x/i.js":      "",
		"server/node_modules/m.js": "",
	})
	got, err := sourceFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "package.json", "src/graphify-out/k", "src/logs", "src/main.ts"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("files = %v\nwant    %v", got, want)
	}
}

func TestSourceFilesWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := tree(t, map[string]string{
		".gitignore":   "dist/\n",
		"tracked.txt":  "",
		"deleted.txt":  "",
		"untracked.go": "",
		"dist/out.js":  "",
	})
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".gitignore", "tracked.txt", "deleted.txt")
	git("commit", "-qm", "init")
	os.Remove(filepath.Join(dir, "deleted.txt"))

	got, err := sourceFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{".gitignore", "tracked.txt", "untracked.go"}; !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

// modes packs files from dir and returns the mode of each entry.
func modes(t *testing.T, dir string, files []string, executable map[string]bool) map[string]int64 {
	t.Helper()
	var buf bytes.Buffer
	if err := pack(dir, files, executable, &buf); err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = h.Mode
	}
}

// A deploy from Windows, where files have no executable bit, takes it from git.
func TestPackTakesTheExecutableBitFromGit(t *testing.T) {
	dir := tree(t, map[string]string{"start.sh": "#!/bin/sh\n", "bin/run": "", "app.js": "", "new.sh": ""})
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	if got := gitExecutable(dir); len(got) != 0 {
		t.Fatalf("outside a repository: %v", got)
	}
	git("init", "-q")
	git("add", "start.sh", "bin/run", "app.js")
	git("update-index", "--chmod=+x", "start.sh", "bin/run")

	exe := gitExecutable(dir)
	if want := map[string]bool{"start.sh": true, "bin/run": true}; !maps.Equal(exe, want) {
		t.Fatalf("gitExecutable = %v, want %v", exe, want)
	}
	got := modes(t, dir, []string{"start.sh", "bin/run", "app.js", "new.sh"}, exe)
	want := map[string]int64{"start.sh": 0o755, "bin/run": 0o755, "app.js": 0o644, "new.sh": 0o644}
	if !maps.Equal(got, want) {
		t.Errorf("modes = %v, want %v", got, want)
	}
}

func TestPackKeepsFileSystemModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bit on Windows file systems")
	}
	dir := tree(t, map[string]string{"start.sh": "", "app.js": ""})
	os.Chmod(filepath.Join(dir, "start.sh"), 0o750)
	got := modes(t, dir, []string{"start.sh", "app.js"}, nil)
	if want := map[string]int64{"start.sh": 0o750, "app.js": 0o644}; !maps.Equal(got, want) {
		t.Errorf("modes = %v, want %v", got, want)
	}
}

func TestCommitHeaders(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	if h := commitHeaders(dir); h != nil {
		t.Fatalf("outside a repository: %v", h)
	}
	git("init", "-q")
	if h := commitHeaders(dir); h != nil {
		t.Fatalf("a repository without commits: %v", h)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "feat: first commit")
	h := commitHeaders(dir)
	if len(h) != 4 || len(h[1]) != 40 || h[3] != "feat:%20first%20commit" {
		t.Fatalf("clean tree: %v", h)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0o644)
	if h := commitHeaders(dir); !strings.HasSuffix(h[1], "-dirty") {
		t.Fatalf("modified tree: %v", h)
	}
}
