package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
