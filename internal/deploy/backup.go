package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/store"
)

// keepBackups is how many dumps are kept per project; older ones are deleted.
// ponytail: backups live on the same disk as the database, which covers bad migrations and
// mistakes, not a lost server. Copy them off-site (S3) when that matters.
const keepBackups = 14

// Backup is one database dump.
type Backup struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// BackupNow dumps the project's Postgres add-on.
func (d *Deployer) BackupNow(ctx context.Context, project string) (Backup, error) {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return Backup{}, err
	}
	return d.backup(ctx, p, "manual")
}

// backup writes BackupDir/<project>/<time>-<reason>.dump with pg_dump's custom format, which
// pg_restore reads. The caller holds the project lock.
func (d *Deployer) backup(ctx context.Context, p store.Project, reason string) (Backup, error) {
	if d.opt.BackupDir == "" {
		return Backup{}, errors.New("backups are not configured")
	}
	dir := filepath.Join(d.opt.BackupDir, p.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Backup{}, err
	}
	name := time.Now().UTC().Format("20060102T150405Z") + "-" + reason + ".dump"
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return Backup{}, err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	_, err = d.rt.Exec(ctx, AddonContainer(p.Name, "postgres"),
		[]string{"pg_dump", "-U", "postgres", "-Fc", "-d", pgDatabase(p.Name)}, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Backup{}, fmt.Errorf("backup: %w", err)
	}
	// Rename last, so a listed dump is always complete.
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return Backup{}, err
	}
	d.pruneBackups(p.Name)
	fi, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return Backup{}, err
	}
	return Backup{Name: name, Size: fi.Size(), CreatedAt: fi.ModTime()}, nil
}

// Backups lists a project's dumps, newest first.
func (d *Deployer) Backups(project string) ([]Backup, error) {
	if d.opt.BackupDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(d.opt.BackupDir, project))
	if errors.Is(err, os.ErrNotExist) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Backup{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".dump") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{Name: e.Name(), Size: fi.Size(), CreatedAt: fi.ModTime()})
	}
	// Names start with a UTC timestamp, so name order is time order.
	slices.SortFunc(out, func(a, b Backup) int { return strings.Compare(b.Name, a.Name) })
	return out, nil
}

// BackupPath returns the file of a project's dump, for download.
func (d *Deployer) BackupPath(project, name string) (string, error) {
	if d.opt.BackupDir == "" || !strings.HasSuffix(name, ".dump") || name != filepath.Base(name) {
		return "", fmt.Errorf("backup %s: %w", name, store.ErrNotFound)
	}
	p := filepath.Join(d.opt.BackupDir, project, name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("backup %s: %w", name, store.ErrNotFound)
	}
	return p, nil
}

func (d *Deployer) pruneBackups(project string) {
	all, err := d.Backups(project)
	if err != nil || len(all) <= keepBackups {
		return
	}
	for _, b := range all[keepBackups:] {
		if err := os.Remove(filepath.Join(d.opt.BackupDir, project, b.Name)); err != nil {
			d.log.Warn("prune backup", "project", project, "backup", b.Name, "err", err)
		}
	}
}

// DailyBackups dumps every project with Postgres whose newest dump is a day old or more.
// Projects busy with a deploy are skipped until the next call.
func (d *Deployer) DailyBackups(ctx context.Context) {
	projects, err := d.store.Projects(ctx)
	if err != nil {
		d.log.Error("daily backups", "err", err)
		return
	}
	for _, p := range projects {
		addons, err := d.store.Addons(ctx, p.ID)
		if err != nil || !slices.ContainsFunc(addons, func(a store.Addon) bool { return a.Kind == "postgres" }) {
			continue
		}
		if all, _ := d.Backups(p.Name); len(all) > 0 && time.Since(all[0].CreatedAt) < 24*time.Hour {
			continue
		}
		l := d.projectLock(p.Name)
		if !l.TryLock() {
			continue
		}
		b, err := d.backup(ctx, p, "daily")
		l.Unlock()
		if err != nil {
			d.log.Error("daily backup", "project", p.Name, "err", err)
			continue
		}
		d.log.Info("daily backup", "project", p.Name, "backup", b.Name, "bytes", b.Size)
	}
}
