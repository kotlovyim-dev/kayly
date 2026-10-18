package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kotlovyim-dev/kayly/internal/store"
)

// keepLog copies everything written to w into the release's log file, so the output of a
// deploy can be read later (`kayly releases log`). The file is best effort: a full disk must
// not fail the deploy that is trying to report it.
// ponytail: logs are never deleted; a build log is tens of KB, so a thousand releases is tens
// of MB. Prune them with their images if that stops being true.
func (d *Deployer) keepLog(rel store.Release, w io.Writer) (io.Writer, func()) {
	if d.opt.LogDir == "" {
		return w, func() {}
	}
	if err := os.MkdirAll(d.opt.LogDir, 0o700); err != nil {
		d.log.Warn("release log", "err", err)
		return w, func() {}
	}
	f, err := os.OpenFile(d.logPath(rel.ID), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		d.log.Warn("release log", "err", err)
		return w, func() {}
	}
	return &teeWriter{primary: w, file: f}, func() { f.Close() }
}

func (d *Deployer) logPath(releaseID int64) string {
	return filepath.Join(d.opt.LogDir, fmt.Sprintf("%d.log", releaseID))
}

// teeWriter writes to the caller first and to the file on the side.
type teeWriter struct {
	primary io.Writer
	file    *os.File
}

func (t *teeWriter) Write(b []byte) (int, error) {
	t.file.Write(b)
	return t.primary.Write(b)
}

// ReleaseLogPath returns the file holding the output of a release's deploy.
func (d *Deployer) ReleaseLogPath(ctx context.Context, project string, seq int) (string, error) {
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return "", err
	}
	rel, err := d.store.Release(ctx, p.ID, seq)
	if err != nil {
		return "", err
	}
	path := d.logPath(rel.ID)
	if d.opt.LogDir == "" {
		return "", fmt.Errorf("log of r%d: %w", seq, store.ErrNotFound)
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("log of r%d: %w", seq, store.ErrNotFound)
	}
	return path, nil
}
