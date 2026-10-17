package api

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
)

// maxSourceBytes bounds the unpacked size of an uploaded source tree.
const maxSourceBytes = 1 << 30

// extract unpacks a gzipped tar into dir. It accepts regular files, directories and
// symlinks that stay inside dir, and rejects anything else: absolute or escaping paths,
// devices, and trees larger than maxSourceBytes.
func extract(r io.Reader, dir string) error {
	// Every write goes through root, which refuses paths that resolve outside dir, also
	// through symlinks unpacked earlier in the same archive.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("source archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("source archive: %w", err)
		}
		name := path.Clean(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("source archive: %q is outside the app", h.Name)
		}
		target := filepath.FromSlash(name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("source archive: %w", err)
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxSourceBytes {
				return fmt.Errorf("source archive: larger than %d MiB unpacked", maxSourceBytes>>20)
			}
			if err := writeFile(root, target, tr, h.FileInfo().Mode().Perm()); err != nil {
				return fmt.Errorf("source archive: %w", err)
			}
		case tar.TypeSymlink:
			// Join drops a leading "/", so absolute targets are checked on their own.
			if filepath.IsAbs(h.Linkname) || !filepath.IsLocal(filepath.Join(path.Dir(name), h.Linkname)) {
				return fmt.Errorf("source archive: symlink %s points outside the app", h.Name)
			}
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("source archive: %w", err)
			}
			if err := root.Symlink(h.Linkname, target); err != nil {
				return fmt.Errorf("source archive: %w", err)
			}
		default:
			return fmt.Errorf("source archive: %s: unsupported entry type", h.Name)
		}
	}
}

func writeFile(root *os.Root, target string, r io.Reader, perm os.FileMode) error {
	if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// O_EXCL: an entry may not overwrite a file or follow a symlink written earlier.
	f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
