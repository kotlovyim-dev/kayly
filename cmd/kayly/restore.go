package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/offsite"
	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// restoreServer is `kayly restore`: on a new host, it brings back the database, the master key
// and the newest backups from the bucket. install.sh has already started the daemon once, which
// created an empty state; that state is stopped and set aside first. A state that holds any
// project is never replaced.
func restoreServer(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	data := fs.String("data", "/var/lib/kayly", "state directory")
	endpoint := fs.String("endpoint", "", "S3 endpoint, e.g. fsn1.your-objectstorage.com")
	region := fs.String("region", "", "S3 region (optional)")
	bucket := fs.String("bucket", "", "bucket name")
	accessKey := fs.String("access-key", "", "S3 access key; the secret key is read from stdin")
	recovery := fs.String("recovery-key", "", "the recovery key printed by `kayly backups setup`")
	fs.Parse(args)
	if *endpoint == "" || *bucket == "" || *accessKey == "" || *recovery == "" {
		return errors.New("usage: kayly restore -endpoint E -bucket B -access-key K -recovery-key KEY [-region R] < secret-key-file")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*recovery))
	if err != nil || len(key) != offsite.KeySize {
		return errors.New("the recovery key is not valid: copy it exactly as `kayly backups setup` printed it")
	}
	secretKey, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if secretKey = strings.TrimSpace(secretKey); secretKey == "" {
		return fmt.Errorf("pipe the S3 secret key on stdin (%v)", err)
	}
	b, err := offsite.OpenS3(offsite.Config{Endpoint: *endpoint, Region: *region, Bucket: *bucket, AccessKey: *accessKey, SecretKey: secretKey})
	if err != nil {
		return err
	}
	ctx := context.Background()

	stopped, err := stopDaemon(ctx)
	if err != nil {
		return err
	}
	if err := setAsideFreshState(ctx, *data); err != nil {
		return err
	}
	say := func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) }
	fmt.Println("restoring from the bucket:")
	if err := offsite.RestoreServer(ctx, b, key, *data, filepath.Join(*data, "backups"), say); err != nil {
		return err
	}
	start := "systemctl start kayly        (it recreates the add-ons, empty)"
	if stopped {
		if out, err := exec.CommandContext(ctx, "systemctl", "start", "kayly").CombinedOutput(); err != nil {
			return fmt.Errorf("the state is restored, but systemctl start kayly failed: %w: %s", err, out)
		}
		start = "(done) the daemon is running again and recreates the add-ons, empty"
	}
	fmt.Printf(`
Done. Next:
  1. %s
  2. kayly deploy -p <project>    from each app's working tree, or push the repo
  3. kayly backups restore latest -p <project>    for every project with data
The tokens are the ones of the restored server; the one install.sh printed is gone.
`, start)
	return nil
}

// stopDaemon stops the kayly unit if it runs, so it lets go of the database, and reports
// whether it did. A host without systemd has nothing to stop.
func stopDaemon(ctx context.Context) (bool, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, nil
	}
	if exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "kayly").Run() != nil {
		return false, nil
	}
	fmt.Println("stopping the kayly daemon (apps keep serving)…")
	if out, err := exec.CommandContext(ctx, "systemctl", "stop", "kayly").CombinedOutput(); err != nil {
		return false, fmt.Errorf("systemctl stop kayly: %w: %s", err, out)
	}
	return true, nil
}

// setAsideFreshState moves the state a fresh install created (a database without projects and
// its key) into <data>/pre-restore-<time>, out of the restore's way. It refuses a database that
// holds projects: restore brings back a lost server, it never replaces a working one.
func setAsideFreshState(ctx context.Context, data string) error {
	db, keyFile := filepath.Join(data, "kayly.db"), filepath.Join(data, "master.key")
	if _, err := os.Stat(db); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	k, err := secret.LoadOrCreateKey(keyFile)
	if err != nil {
		return err
	}
	box, err := secret.NewBox(k)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, db, box)
	if err != nil {
		return fmt.Errorf("open the existing state: %w", err)
	}
	projects, err := st.Projects(ctx)
	st.Close()
	if err != nil {
		return fmt.Errorf("read the existing state: %w", err)
	}
	if len(projects) > 0 {
		return fmt.Errorf("%s holds %d projects: kayly restore brings a lost server back onto a fresh install and never replaces a working one; "+
			"to put one project's data back, use kayly backups restore", db, len(projects))
	}
	aside := filepath.Join(data, "pre-restore-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := store.SetAside(db, aside); err != nil {
		return err
	}
	if err := os.Rename(keyFile, filepath.Join(aside, "master.key")); err != nil {
		return fmt.Errorf("set aside %s: %w", keyFile, err)
	}
	fmt.Printf("set the fresh install's empty state aside in %s\n", aside)
	return nil
}
