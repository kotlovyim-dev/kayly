package deploy

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// keepBackups is how many backups are kept per project and kind; older ones are deleted.
// ponytail: backups live on the same disk as the data, which covers bad migrations and
// mistakes, not a lost server. Copy them off-site (S3) when that matters.
const keepBackups = 14

// toolImage runs `tar` over a volume. Pinned like every other image.
const toolImage = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"

// backupName matches <UTC time>-<reason>.<kind>, where kind is dump (Postgres), rdb (Redis)
// kayly.tar (Kayly's own database and key) or vol-<name>.tar (a volume).
var backupName = regexp.MustCompile(`^\d{8}T\d{6}Z-[a-z0-9-]+\.(dump|rdb|kayly\.tar|vol-[a-z0-9-]+\.tar)$`)

// Backup is one backup file.
type Backup struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// kind is what a backup holds: its name after the first dot, e.g. "dump" or "vol-uploads.tar".
func (b Backup) kind() string { return b.Name[strings.IndexByte(b.Name, '.')+1:] }

// target is one thing to back up. dump writes its backup to the file at path.
type target struct {
	kind string
	dump func(ctx context.Context, path string) error
}

// targets lists what a project has to back up: its Postgres and Redis add-ons and the
// volumes of its live release.
func (d *Deployer) targets(ctx context.Context, p store.Project) ([]target, error) {
	addons, err := d.store.Addons(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var out []target
	for _, a := range addons {
		switch a.Kind {
		case "postgres":
			out = append(out, d.postgresTarget(p.Name))
		case "redis":
			out = append(out, d.execTarget(p.Name, a.Kind, "rdb", "sh", "-c", rdbSnapshot, a.Password))
		}
	}
	if p.ActiveRelease == 0 {
		return out, nil
	}
	rel, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(rel.Config)
	if err != nil {
		return nil, fmt.Errorf("r%d config: %w", rel.Seq, err)
	}
	seen := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		for _, v := range cfg.Services[name].Volumes {
			if !seen[v.Name] {
				seen[v.Name] = true
				out = append(out, d.volumeTarget(p.Name, v.Name))
			}
		}
	}
	return out, nil
}

// rdbSnapshot writes a snapshot with valkey-cli into a temporary file and prints it ($0 is the
// password). `--rdb -` would append the replication stream's EOF marker, which an AOF base
// file may not carry.
const rdbSnapshot = `set -e; f=$(mktemp); trap 'rm -f "$f"' EXIT; valkey-cli --no-auth-warning -a "$0" --rdb "$f" >&2; cat "$f"`

// execTarget streams the stdout of cmd, run in the add-on's container, into the backup file.
func (d *Deployer) execTarget(project, addon, kind string, cmd ...string) target {
	return target{kind: kind, dump: func(ctx context.Context, path string) error {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return err
		}
		_, err = d.rt.Exec(ctx, AddonContainer(project, addon), cmd, nil, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}}
}

// volumeTarget tars a volume from a throwaway container that mounts it read-only. The
// container writes straight into the backup directory.
func (d *Deployer) volumeTarget(project, volume string) target {
	return target{kind: "vol-" + volume + ".tar", dump: func(ctx context.Context, path string) error {
		return d.runTool(ctx, project, project+"_backup_"+volume,
			[]string{"tar", "-C", "/data", "-cf", "/out/" + filepath.Base(path), "."},
			[]Mount{
				{Volume: VolumeName(project, volume), Path: "/data", ReadOnly: true},
				{Bind: filepath.Dir(path), Path: "/out"},
			})
	}}
}

// runTool runs cmd to completion in a throwaway toolImage container named name and returns
// its last output lines if it fails. The container may write files it does not own, so it
// keeps the capabilities that root needs for that.
func (d *Deployer) runTool(ctx context.Context, project, name string, cmd []string, mounts []Mount) error {
	spec := Spec{
		Name: name, Image: toolImage, Cmd: cmd, Mounts: mounts,
		CapAdd: []string{"DAC_OVERRIDE", "CHOWN", "FOWNER"},
		Labels: labels(project, 0, RoleBackup, ""),
	}
	cctx := context.WithoutCancel(ctx)
	if err := d.rt.Remove(cctx, name, 0); err != nil { // left over from a crash
		return err
	}
	defer d.rt.Remove(cctx, name, 0)
	if ok, err := d.rt.ImageExists(ctx, spec.Image); err != nil {
		return err
	} else if !ok {
		if err := d.rt.PullImage(ctx, spec.Image); err != nil {
			return err
		}
	}
	if err := d.rt.Run(ctx, spec); err != nil {
		return err
	}
	code, err := d.rt.Wait(ctx, name)
	if err != nil {
		return err
	}
	if code != 0 {
		var out strings.Builder
		d.rt.Logs(cctx, name, false, 5, &out)
		return fmt.Errorf("%s exited %d: %s", cmd[0], code, strings.TrimSpace(out.String()))
	}
	return nil
}

// BackupNow backs up everything the project has: databases and volumes. It keeps going
// after a failure, so one broken target does not hide the others.
func (d *Deployer) BackupNow(ctx context.Context, project string) ([]Backup, error) {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	return d.backupAll(ctx, p, "manual")
}

func (d *Deployer) backupAll(ctx context.Context, p store.Project, reason string) ([]Backup, error) {
	ts, err := d.targets(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []Backup
	var errs []error
	for _, t := range ts {
		b, err := d.write(ctx, p, reason, t)
		if err != nil {
			d.alert(ctx, "✗ %s: %s backup failed: %v", p.Name, reason, err)
			errs = append(errs, err)
			continue
		}
		out = append(out, b)
	}
	return out, errors.Join(errs...)
}

// backup dumps the Postgres add-on before a release step. The caller holds the project lock.
func (d *Deployer) backup(ctx context.Context, p store.Project, reason string) (Backup, error) {
	return d.write(ctx, p, reason, d.postgresTarget(p.Name))
}

func (d *Deployer) postgresTarget(project string) target {
	return d.execTarget(project, "postgres", "dump", "pg_dump", "-U", "postgres", "-Fc", "-d", pgDatabase(project))
}

// write creates BackupDir/<project>/<time>-<reason>.<kind>. The file appears under its name
// only once complete, so a listed backup is always whole.
func (d *Deployer) write(ctx context.Context, p store.Project, reason string, t target) (Backup, error) {
	if d.opt.BackupDir == "" {
		return Backup{}, errors.New("backups are not configured")
	}
	dir := filepath.Join(d.opt.BackupDir, p.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Backup{}, err
	}
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return Backup{}, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name()) // no-op after the rename
	if err := t.dump(ctx, tmp.Name()); err != nil {
		return Backup{}, fmt.Errorf("backup %s: %w", t.kind, err)
	}
	name := time.Now().UTC().Format("20060102T150405Z") + "-" + reason + "." + t.kind
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

// Backups lists a project's backups, newest first.
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
		if !backupName.MatchString(e.Name()) {
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

// BackupPath returns the file of a project's backup, for download.
func (d *Deployer) BackupPath(project, name string) (string, error) {
	if d.opt.BackupDir == "" || !backupName.MatchString(name) {
		return "", fmt.Errorf("backup %s: %w", name, store.ErrNotFound)
	}
	p := filepath.Join(d.opt.BackupDir, project, name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("backup %s: %w", name, store.ErrNotFound)
	}
	return p, nil
}

// pruneBackups keeps the newest keepBackups of each kind.
func (d *Deployer) pruneBackups(project string) {
	all, err := d.Backups(project)
	if err != nil {
		return
	}
	n := map[string]int{}
	for _, b := range all {
		if n[b.kind()]++; n[b.kind()] <= keepBackups {
			continue
		}
		if err := os.Remove(filepath.Join(d.opt.BackupDir, project, b.Name)); err != nil {
			d.log.Warn("prune backup", "project", project, "backup", b.Name, "err", err)
		}
	}
}

// DailyBackups backs up every target whose newest backup is a day old or more. Projects busy
// with a deploy are skipped until the next call.
func (d *Deployer) DailyBackups(ctx context.Context) {
	projects, err := d.store.Projects(ctx)
	if err != nil {
		d.log.Error("daily backups", "err", err)
		return
	}
	for _, p := range projects {
		l := d.projectLock(p.Name)
		if !l.TryLock() {
			continue
		}
		d.dailyBackups(ctx, p)
		l.Unlock()
	}
	if d.opt.KeyFile != "" {
		d.dailyTargets(ctx, store.Project{Name: KaylyBackups}, []target{d.kaylyTarget()})
	}
}

func (d *Deployer) dailyBackups(ctx context.Context, p store.Project) {
	ts, err := d.targets(ctx, p)
	if err != nil {
		d.log.Error("daily backup", "project", p.Name, "err", err)
		d.alert(ctx, "✗ %s: daily backup failed: %v", p.Name, err)
		return
	}
	d.dailyTargets(ctx, p, ts)
}

// dailyTargets backs up the targets whose newest backup is a day old or more.
func (d *Deployer) dailyTargets(ctx context.Context, p store.Project, ts []target) {
	existing, _ := d.Backups(p.Name)
	newest := map[string]time.Time{}
	for _, b := range existing { // newest first
		if _, ok := newest[b.kind()]; !ok {
			newest[b.kind()] = b.CreatedAt
		}
	}
	for _, t := range ts {
		if time.Since(newest[t.kind]) < 24*time.Hour {
			continue
		}
		b, err := d.write(ctx, p, "daily", t)
		if err != nil {
			d.log.Error("daily backup", "project", p.Name, "err", err)
			d.alert(ctx, "✗ %s: daily backup failed: %v", p.Name, err)
			continue
		}
		d.log.Info("daily backup", "project", p.Name, "backup", b.Name, "bytes", b.Size)
	}
}

// KaylyBackups is the backup directory of Kayly itself. It is not a valid project name.
const KaylyBackups = "_kayly"

// SnapshotKayly backs up Kayly's own state: a consistent copy of the database and the master
// key, which together restore every project's env and add-on credentials.
func (d *Deployer) SnapshotKayly(ctx context.Context, reason string) (Backup, error) {
	return d.write(ctx, store.Project{Name: KaylyBackups}, reason, d.kaylyTarget())
}

func (d *Deployer) kaylyTarget() target {
	return target{kind: "kayly.tar", dump: func(ctx context.Context, path string) error {
		snap := path + ".db"
		defer os.Remove(snap)
		if err := d.store.Snapshot(ctx, snap); err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return err
		}
		tw := tar.NewWriter(out)
		for _, f := range []struct{ name, path string }{{"kayly.db", snap}, {"master.key", d.opt.KeyFile}} {
			if err = addFile(tw, f.name, f.path); err != nil {
				break
			}
		}
		if cerr := tw.Close(); err == nil {
			err = cerr
		}
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}}
}

func addFile(tw *tar.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
