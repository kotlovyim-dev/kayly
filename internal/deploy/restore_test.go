package deploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeOffsite is a bucket of decrypted backups: project → name → contents.
type fakeOffsite struct {
	files map[string]map[string]string
	fail  error
}

func (f *fakeOffsite) Enabled(context.Context) (bool, error) { return true, nil }

func (f *fakeOffsite) List(_ context.Context, project string) ([]Backup, error) {
	var out []Backup
	for name, body := range f.files[project] {
		out = append(out, Backup{Name: name, Size: int64(len(body)), CreatedAt: time.Now()})
	}
	slices.SortFunc(out, func(a, b Backup) int { return strings.Compare(b.Name, a.Name) })
	return out, nil
}

func (f *fakeOffsite) Open(_ context.Context, project, name string) (io.ReadCloser, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	body, ok := f.files[project][name]
	if !ok {
		return nil, errors.New("no such object")
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (h *harness) withAddons(kinds ...string) {
	h.t.Helper()
	h.mustDeploy()
	for _, k := range kinds {
		if _, err := h.d.AddAddon(context.Background(), "app", k); err != nil {
			h.t.Fatal(err)
		}
	}
}

// backupOf returns the newest local backup name ending in suffix.
func (h *harness) backupOf(suffix string) string {
	h.t.Helper()
	all, err := h.d.Backups("app")
	if err != nil {
		h.t.Fatal(err)
	}
	for _, b := range all {
		if strings.HasSuffix(b.Name, suffix) {
			return b.Name
		}
	}
	h.t.Fatalf("no %s backup in %v", suffix, all)
	return ""
}

func TestRestorePostgresFeedsTheDumpAndBacksUpFirst(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("postgres")
	if _, err := h.d.BackupNow(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	name := h.backupOf(".dump")
	h.rt.execs = nil
	var log strings.Builder
	if err := h.d.Restore(ctx, "app", name, RestoreSource{}, &log); err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	// First the safety dump, then pg_restore reading the chosen file on stdin.
	if len(h.rt.execs) != 2 || h.rt.execs[0].cmd[0] != "pg_dump" || h.rt.execs[1].cmd[0] != "pg_restore" {
		t.Fatalf("execs = %+v", h.rt.execs)
	}
	want, _ := os.ReadFile(filepath.Join(h.backups, "app", name))
	if h.rt.execs[1].input != string(want) || !slices.Contains(h.rt.execs[1].cmd, "--single-transaction") {
		t.Fatalf("pg_restore got %q (%v)", h.rt.execs[1].input, h.rt.execs[1].cmd)
	}
	h.backupOf("-before-restore.dump")
}

func TestFailedRestoreNamesTheSafetyBackupAndAlerts(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.BackupNow(ctx, "app")
	name := h.backupOf(".dump")
	h.rt.execs = nil
	h.rt.hook = func(method, arg string) error {
		if method == "Exec" && len(h.rt.execs) == 1 { // the safety dump is the first Exec
			return errors.New("pg_restore: boom")
		}
		return nil
	}
	err := h.d.Restore(ctx, "app", name, RestoreSource{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "before-restore") {
		t.Fatalf("err = %v", err)
	}
	if len(h.alerts) == 0 || !strings.Contains(h.alerts[len(h.alerts)-1], "restore") {
		t.Fatalf("alerts = %q", h.alerts)
	}
}

func TestRestoreRedisSwapsTheLogAndRestartsTheAddon(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("redis")
	h.d.BackupNow(ctx, "app")
	name := h.backupOf(".rdb")
	h.rt.removed = nil
	var ran []string
	h.rt.hook = func(method, arg string) error {
		if method == "Run" {
			ran = append(ran, arg)
		}
		return nil
	}
	if err := h.d.Restore(ctx, "app", name, RestoreSource{}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ran, "app_restore_redis") {
		t.Errorf("the log was not rewritten: ran %v", ran)
	}
	if !slices.Contains(h.rt.removed, "app_redis") {
		t.Errorf("the add-on was not stopped: removed %v", h.rt.removed)
	}
	if c, err := h.rt.Inspect(ctx, "app_redis"); err != nil || !c.Running {
		t.Errorf("the add-on is not running again: %+v, %v", c, err)
	}
}

func TestRestoreVolumeStopsItsServicesAndTheReconcilerBringsThemBack(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	h.d.BackupNow(ctx, "app")
	name := h.backupOf(".vol-data.tar")
	h.rt.removed = nil
	if err := h.d.Restore(ctx, "app", name, RestoreSource{}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.rt.removed, "app_web_r1") {
		t.Errorf("web mounts the volume and should have been stopped: %v", h.rt.removed)
	}
	h.assertLive(1)
}

func TestRestoreFromOffsiteIntoAnotherProject(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.opt.Offsite = &fakeOffsite{files: map[string]map[string]string{
		"leryo": {"20260101T000000Z-daily.dump": "leryo database"},
	}}
	h.rt.execs = nil
	err := h.d.Restore(ctx, "app", "20260101T000000Z-daily.dump", RestoreSource{Project: "leryo", Remote: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if last := h.rt.execs[len(h.rt.execs)-1]; last.cmd[0] != "pg_restore" || last.input != "leryo database" {
		t.Fatalf("last exec = %+v", last)
	}
	// The download is scratch: it must not stay where the uploader or the list could find it.
	entries, _ := os.ReadDir(filepath.Join(h.backups, "app"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("left %s behind", e.Name())
		}
	}
}

func TestFailedDownloadTouchesNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.opt.Offsite = &fakeOffsite{fail: errors.New("bucket unreachable")}
	h.rt.execs = nil
	err := h.d.Restore(ctx, "app", "20260101T000000Z-daily.dump", RestoreSource{Remote: true}, io.Discard)
	if err == nil || len(h.rt.execs) != 0 {
		t.Fatalf("err = %v, execs = %+v", err, h.rt.execs)
	}
	if all, _ := h.d.Backups("app"); len(all) != 0 {
		t.Errorf("a safety backup was taken before the download succeeded: %v", all)
	}
}

func TestRestoreRefusesWhatItCannotRestoreInto(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	for _, name := range []string{
		"20260101T000000Z-daily.rdb",       // no redis add-on
		"20260101T000000Z-daily.kayly.tar", // a server backup
		"../../etc/passwd",
	} {
		if err := h.d.Restore(ctx, "app", name, RestoreSource{Remote: false}, io.Discard); err == nil {
			t.Errorf("%s: restore succeeded", name)
		}
	}
}

func TestRestoreRefusesASourceThatIsNotAProjectName(t *testing.T) {
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.opt.Offsite = &fakeOffsite{files: map[string]map[string]string{"app": {"20260101T000000Z-daily.dump": "x"}}}
	for _, from := range []string{"../app", "app/../../etc", "A B", "/etc"} {
		for _, remote := range []bool{false, true} {
			err := h.d.Restore(context.Background(), "app", "20260101T000000Z-daily.dump", RestoreSource{Project: from, Remote: remote}, io.Discard)
			if err == nil {
				t.Errorf("from %q (remote %v) accepted", from, remote)
			}
		}
	}
	if len(h.rt.execs) != 0 {
		t.Errorf("a refused restore still ran %+v", h.rt.execs)
	}
}
