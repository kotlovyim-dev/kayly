package offsite

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// serverProject is the bucket directory of Kayly's own backups (deploy.KaylyBackups).
const serverProject = "_kayly"

// RestoreServer brings a lost server back from the bucket, for a host where the daemon is not
// running yet: the newest Kayly backup supplies kayly.db and master.key in dataDir, and the
// newest backup of every project and kind lands, decrypted, in backupDir so that
// `kayly backups restore` can put it into the add-ons once the daemon has recreated them.
// It refuses to overwrite an existing database.
func RestoreServer(ctx context.Context, b Bucket, recoveryKey []byte, dataDir, backupDir string, say func(string, ...any)) error {
	if _, err := os.Stat(filepath.Join(dataDir, "kayly.db")); err == nil {
		return fmt.Errorf("%s/kayly.db exists: restore into a fresh server", dataDir)
	}
	listed, err := b.List(ctx)
	if err != nil {
		return fmt.Errorf("list bucket: %w", err)
	}
	var objs []Object
	for _, o := range listed {
		name, ok := strings.CutSuffix(o.Key, ".enc")
		_, file, _ := strings.Cut(name, "/")
		if t, err := time.Parse("20060102T150405Z", file[:min(len(file), 16)]); err == nil && ok {
			objs = append(objs, Object{Key: o.Key, Name: name, Time: t})
		}
	}
	newest := map[string]Object{} // by project/kind
	for _, o := range objs {
		if cur, ok := newest[o.kind()]; !ok || o.Time.After(cur.Time) {
			newest[o.kind()] = o
		}
	}

	snap, ok := newest[serverProject+"/kayly.tar"]
	if !ok {
		return errors.New("the bucket holds no Kayly backup: nothing to restore the server from")
	}
	say("Kayly state: %s", snap.Name)
	if err := restoreState(ctx, b, recoveryKey, snap.Key, dataDir); err != nil {
		return err
	}
	for _, kind := range slices.Sorted(maps.Keys(newest)) {
		o := newest[kind]
		if strings.HasPrefix(kind, serverProject+"/") {
			continue
		}
		project, file, _ := strings.Cut(o.Name, "/")
		say("%s: %s", project, file)
		if err := download(ctx, b, recoveryKey, o.Key, filepath.Join(backupDir, project, file)); err != nil {
			return fmt.Errorf("%s: %w", o.Name, err)
		}
	}
	return nil
}

// restoreState unpacks a Kayly backup into dataDir. The database and the key are written
// together or not at all.
func restoreState(ctx context.Context, b Bucket, recoveryKey []byte, key, dataDir string) error {
	rc, err := OpenObject(ctx, b, key, recoveryKey)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	var written []string
	undo := func() {
		for _, p := range written {
			os.Remove(p)
		}
	}
	got := map[string]bool{}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			undo()
			return fmt.Errorf("read Kayly backup: %w", err)
		}
		if hdr.Name != "kayly.db" && hdr.Name != "master.key" {
			continue
		}
		dst := filepath.Join(dataDir, hdr.Name)
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			undo()
			return err
		}
		written = append(written, dst)
		_, err = io.Copy(f, tr)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			undo()
			return fmt.Errorf("write %s: %w", hdr.Name, err)
		}
		got[hdr.Name] = true
	}
	if !got["kayly.db"] || !got["master.key"] {
		undo()
		return errors.New("the Kayly backup lacks kayly.db or master.key")
	}
	return nil
}

// download decrypts an object into path, which appears only once complete.
func download(ctx context.Context, b Bucket, recoveryKey []byte, key, path string) error {
	rc, err := OpenObject(ctx, b, key, recoveryKey)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".partial-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, rc)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
