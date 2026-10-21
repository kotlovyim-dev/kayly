package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// sourceFiles lists the files of the app in dir, relative and slash-separated. In a git
// work tree that is what git would commit (tracked plus untracked, minus ignored);
// elsewhere the tree is walked with the top-level .gitignore applied.
func sourceFiles(dir string) ([]string, error) {
	out, err := exec.Command("git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err == nil {
		var files []string
		for _, f := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
			// Tracked files deleted in the work tree are still listed by --cached.
			if _, err := os.Lstat(filepath.Join(dir, f)); f != "" && err == nil {
				files = append(files, f)
			}
		}
		return files, nil
	}
	ign, err := readGitignore(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if ign.match(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

// gitignore is the subset of .gitignore Kayly applies outside git work trees: comments,
// "dir/" for directories only, a leading or inner "/" to anchor a pattern at the root,
// and path.Match globs.
// ponytail: no "!" negation and no "**"; for those, deploy from a git work tree.
type gitignore []ignoreRule

type ignoreRule struct {
	pattern  string
	anchored bool
	dirOnly  bool
}

// alwaysIgnored never belongs in a build context.
var alwaysIgnored = gitignore{{pattern: ".git"}, {pattern: "node_modules", dirOnly: true}}

func readGitignore(file string) (gitignore, error) {
	rules := alwaysIgnored
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return rules, nil
	}
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		r := ignoreRule{}
		r.dirOnly = strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		r.anchored = strings.Contains(line, "/")
		r.pattern = strings.TrimPrefix(line, "/")
		rules = append(rules, r)
	}
	return rules, sc.Err()
}

func (g gitignore) match(rel string, isDir bool) bool {
	for _, r := range g {
		if r.dirOnly && !isDir {
			continue
		}
		name := path.Base(rel)
		if r.anchored {
			name = rel
		}
		if ok, _ := path.Match(r.pattern, name); ok {
			return true
		}
	}
	return false
}

// gitExecutable is the set of files git's index records as executable (mode 100755) in dir.
// Windows file systems have no executable bit, so a deploy from Windows takes it from git.
// Outside a git work tree the set is empty.
func gitExecutable(dir string) map[string]bool {
	set := map[string]bool{}
	out, err := exec.Command("git", "-C", dir, "ls-files", "--stage", "-z").Output()
	if err != nil {
		return set
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		// "<mode> <object> <stage>\t<path>"
		meta, file, ok := strings.Cut(entry, "\t")
		if ok && strings.HasPrefix(meta, "100755 ") {
			set[file] = true
		}
	}
	return set
}

// pack writes files from dir as a gzipped tar to w. Regular files keep their mode on the
// file system, unless executable is set: then it alone decides between 0755 and 0644.
func pack(dir string, files []string, executable map[string]bool, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		full := filepath.Join(dir, filepath.FromSlash(f))
		fi, err := os.Lstat(full)
		if err != nil {
			return err
		}
		link := ""
		if fi.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(full); err != nil {
				return err
			}
		} else if !fi.Mode().IsRegular() {
			continue
		}
		h, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		h.Name = f
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
		if executable != nil && fi.Mode().IsRegular() {
			h.Mode = 0o644
			if executable[f] {
				h.Mode = 0o755
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			if err := copyFile(tw, full); err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func copyFile(w io.Writer, name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// commitHeaders describes the git commit of dir for the server, as request headers. Without
// git, outside a repository or in one without commits it sends nothing. A work tree with
// uncommitted changes is marked "-dirty": the release is not exactly that commit.
func commitHeaders(dir string) []string {
	git := func(args ...string) (string, bool) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	sha, ok := git("rev-parse", "HEAD")
	if !ok {
		return nil
	}
	if status, ok := git("status", "--porcelain"); ok && status != "" {
		sha += "-dirty"
	}
	msg, _ := git("log", "-1", "--format=%s")
	return []string{"X-Kayly-Commit", sha, "X-Kayly-Commit-Message", url.PathEscape(msg)}
}
