package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// Offsite is the owner's bucket, as far as restores and drills need it.
type Offsite interface {
	// Enabled reports whether a bucket is configured.
	Enabled(ctx context.Context) (bool, error)
	// List returns a project's remote backups, newest first.
	List(ctx context.Context, project string) ([]Backup, error)
	// Open returns the decrypted contents of a remote backup.
	Open(ctx context.Context, project, name string) (io.ReadCloser, error)
}

// RestoreSource says where a restore reads from.
type RestoreSource struct {
	// Project is the project the backup was taken from. Empty means the one restored into,
	// so a backup of leryo can be restored into a throwaway project.
	Project string
	// Remote reads the bucket instead of the local backup directory.
	Remote bool
}

// Restore puts a backup back into the project's live add-on or volume. It takes a fresh
// backup of what it is about to replace first, and downloads the whole backup before
// touching anything, so a failure leaves the data as it was. It holds the project lock.
func (d *Deployer) Restore(ctx context.Context, project, name string, src RestoreSource, w io.Writer) error {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	w = &syncWriter{w: w}

	p, err := d.store.Project(ctx, project)
	if err != nil {
		return err
	}
	if src.Project == "" {
		src.Project = project
	}
	if !backupName.MatchString(name) {
		return fmt.Errorf("%q is not a backup name: %w", name, store.ErrNotFound)
	}
	kind := name[strings.IndexByte(name, '.')+1:]
	if kind == "kayly.tar" {
		return errors.New("a Kayly backup restores a whole server: use `kayly restore` on the host")
	}

	step(w, "fetch "+name)
	path, cleanup, err := d.fetch(ctx, p, name, src)
	if err != nil {
		return err
	}
	defer cleanup()

	ts, err := d.targets(ctx, p)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ts, func(t target) bool { return t.kind == kind })
	if i < 0 {
		return fmt.Errorf("%s has nothing to restore a %s backup into: attach the add-on or deploy the volume first", project, kind)
	}
	step(w, "back up what is about to be replaced")
	b, err := d.write(ctx, p, "before-restore", ts[i])
	if err != nil {
		return fmt.Errorf("pre-restore backup: %w", err)
	}
	fmt.Fprintf(w, "%s\n", b.Name)

	step(w, "restore "+kind)
	switch {
	case kind == "dump":
		err = d.restorePostgres(ctx, p, path)
	case kind == "rdb":
		err = d.restoreRedis(ctx, p, path)
	default: // vol-<name>.tar
		err = d.restoreVolume(ctx, p, strings.TrimSuffix(strings.TrimPrefix(kind, "vol-"), ".tar"), path)
	}
	if err != nil {
		d.alert(ctx, "✗ %s: restore of %s failed: %v", project, name, err)
		return fmt.Errorf("restore %s: %w (the previous data is in %s)", name, err, b.Name)
	}
	fmt.Fprintf(w, "\n✓ %s restored\n", name)
	return nil
}

// RemoteBackups lists a project's backups in the owner's bucket, newest first.
func (d *Deployer) RemoteBackups(ctx context.Context, project string) ([]Backup, error) {
	if d.opt.Offsite == nil {
		return nil, errors.New("off-site backups are not set up")
	}
	return d.opt.Offsite.List(ctx, project)
}

// fetch returns a local file holding the backup. A remote backup is downloaded and decrypted
// into the backup directory (the same disk, so large backups do not fill /tmp).
func (d *Deployer) fetch(ctx context.Context, p store.Project, name string, src RestoreSource) (path string, cleanup func(), err error) {
	if !src.Remote {
		path, err = d.BackupPath(src.Project, name)
		return path, func() {}, err
	}
	if d.opt.Offsite == nil {
		return "", nil, errors.New("off-site backups are not set up")
	}
	dir := filepath.Join(d.opt.BackupDir, p.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	tmp, err := os.CreateTemp(dir, ".restore-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.Remove(tmp.Name()) }
	rc, err := d.opt.Offsite.Open(ctx, src.Project, name)
	if err == nil {
		_, err = io.Copy(tmp, rc)
		rc.Close()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("download %s: %w", name, err)
	}
	return tmp.Name(), cleanup, nil
}

// restorePostgres replaces the database's contents in one transaction: if the dump is bad,
// the database is as it was.
func (d *Deployer) restorePostgres(ctx context.Context, p store.Project, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = d.rt.Exec(ctx, AddonContainer(p.Name, "postgres"),
		[]string{"pg_restore", "-U", "postgres", "--clean", "--if-exists", "--no-owner", "--single-transaction", "-d", pgDatabase(p.Name)},
		f, io.Discard)
	return err
}

// restoreRedis stops the add-on, writes the snapshot into its volume as the base file of the
// append-only log (Valkey ignores dump.rdb while the log is on) and starts it again.
func (d *Deployer) restoreRedis(ctx context.Context, p store.Project, path string) error {
	ctx = context.WithoutCancel(ctx) // half a restore is worse than a slow one
	if err := d.rt.Remove(ctx, AddonContainer(p.Name, "redis"), addonKinds["redis"].stopGrace); err != nil {
		return err
	}
	// The new log is built beside the old one and swapped in last.
	const script = `set -e
mkdir /data/.restore
cp /src/"$0" /data/.restore/appendonly.aof.1.base.rdb
: > /data/.restore/appendonly.aof.1.incr.aof
printf 'file appendonly.aof.1.base.rdb seq 1 type b\nfile appendonly.aof.1.incr.aof seq 1 type i\n' > /data/.restore/appendonly.aof.manifest
rm -rf /data/appendonlydir /data/dump.rdb
mv /data/.restore /data/appendonlydir`
	err := d.runTool(ctx, p.Name, p.Name+"_restore_redis", []string{"sh", "-c", script, filepath.Base(path)}, []Mount{
		{Volume: VolumeName(p.Name, "redis"), Path: "/data"},
		{Bind: filepath.Dir(path), Path: "/src", ReadOnly: true},
	})
	// Start the add-on again even if the swap failed: an unreachable database is worse.
	if serr := d.ensureAddons(ctx, p, true); err == nil {
		err = serr
	}
	return err
}

// restoreVolume empties the volume and unpacks the archive into it, with the services that
// mount it stopped. The reconciler (called last) starts them again.
func (d *Deployer) restoreVolume(ctx context.Context, p store.Project, volume, path string) error {
	ctx = context.WithoutCancel(ctx)
	rel, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(rel.Config)
	if err != nil {
		return err
	}
	for name, svc := range cfg.Services {
		if slices.ContainsFunc(svc.Volumes, func(v config.Volume) bool { return v.Name == volume }) {
			if err := d.rt.Remove(ctx, ContainerName(p.Name, name, rel.Seq), d.opt.StopGrace); err != nil {
				return err
			}
		}
	}
	// The archive is read to the end before anything is deleted.
	const script = `set -e
tar -tf /src/"$0" > /dev/null
rm -rf /data/* /data/.[!.]* /data/..?*
tar -xf /src/"$0" -C /data`
	err = d.runTool(ctx, p.Name, p.Name+"_restore_"+volume, []string{"sh", "-c", script, filepath.Base(path)}, []Mount{
		{Volume: VolumeName(p.Name, volume), Path: "/data"},
		{Bind: filepath.Dir(path), Path: "/src", ReadOnly: true},
	})
	if rerr := d.reconcileProject(ctx, p.Name); err == nil {
		err = rerr
	}
	return err
}
