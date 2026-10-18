package deploy

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

func TestAddonFieldsMatchConfig(t *testing.T) {
	if got, want := AddonKindNames(), slices.Sorted(maps.Keys(config.AddonFields)); !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, config knows %v", got, want)
	}
	for _, kind := range AddonKindNames() {
		f := addonFields("my-app", store.Addon{Kind: kind, Password: "pw"})
		if got, want := slices.Sorted(maps.Keys(f)), slices.Sorted(slices.Values(config.AddonFields[kind])); !slices.Equal(got, want) {
			t.Errorf("%s fields = %v, config allows %v", kind, got, want)
		}
	}
	if got := addonFields("my-app", store.Addon{Kind: "postgres", Password: "pw"})["url"]; got != "postgres://postgres:pw@postgres:5432/my_app" {
		t.Errorf("postgres url = %s", got)
	}
	if got := addonFields("my-app", store.Addon{Kind: "redis", Password: "pw"})["url"]; got != "redis://default:pw@redis:6379" {
		t.Errorf("redis url = %s", got)
	}
}

func TestAddonsAreInjectedAndKept(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, kind := range []string{"postgres", "redis"} {
		if _, err := h.d.AddAddon(ctx, "app", kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.d.AddAddon(ctx, "app", "redis"); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Errorf("second redis: %v", err)
	}
	if _, err := h.d.AddAddon(ctx, "app", "mysql"); err == nil || !strings.Contains(err.Error(), "available: postgres, redis") {
		t.Errorf("unknown kind: %v", err)
	}
	h.mustDeploy()
	h.assertLive(1)

	web := h.rt.env("app_web_r1")
	if !strings.HasPrefix(web["DATABASE_URL"], "postgres://postgres:") || !strings.HasPrefix(web["REDIS_URL"], "redis://default:") {
		t.Errorf("web env = %v", web)
	}
	if h.rt.env("app_release_r1") != nil {
		t.Error("release container left behind")
	}
	for _, name := range []string{"app_postgres", "app_redis"} {
		if !slices.Contains(h.rt.names(), name) {
			t.Errorf("%s not running", name)
		}
	}

	// The reconciler keeps add-ons: it restarts a vanished one and never removes them.
	h.rt.mu.Lock()
	delete(h.rt.containers, "app_redis")
	h.rt.mu.Unlock()
	if err := h.d.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.rt.names(), "app_redis") || !slices.Contains(h.rt.names(), "app_postgres") {
		t.Errorf("after reconcile: %v", h.rt.names())
	}

	views, err := h.d.Addons(ctx, "app")
	if err != nil || len(views) != 2 || views[0].Status != "healthy" || views[0].EnvVar != "DATABASE_URL" {
		t.Errorf("views = %+v, %v", views, err)
	}
}

func TestBackupBeforeReleaseStep(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.AddAddon(context.Background(), "app", "postgres"); err != nil {
		t.Fatal(err)
	}
	var order []string
	h.rt.hook = func(method, arg string) error {
		if method == "Exec" || (method == "Run" && strings.Contains(arg, "release")) {
			order = append(order, method)
		}
		return nil
	}
	h.mustDeploy()
	if !slices.Equal(order, []string{"Exec", "Run"}) {
		t.Errorf("order = %v, want the dump before the release step", order)
	}
	all, err := h.d.Backups("app")
	if err != nil || len(all) != 1 || !strings.HasSuffix(all[0].Name, "-before-r1.dump") || all[0].Size == 0 {
		t.Fatalf("backups = %+v, %v", all, err)
	}
	if _, err := h.d.BackupPath("app", "../../etc/passwd"); err == nil {
		t.Error("BackupPath accepted a path outside the backup directory")
	}
}

func TestFailedBackupStopsTheRelease(t *testing.T) {
	h := newHarness(t)
	h.mustDeploy()
	if _, err := h.d.AddAddon(context.Background(), "app", "postgres"); err != nil {
		t.Fatal(err)
	}
	h.rt.hook = failOn("Exec", "app_postgres")
	if _, log, err := h.deploy(); err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("err = %v\n%s", err, log)
	}
	h.rt.hook = nil
	h.assertLive(1)
}

func TestRedeployAppliesEnvWithoutBuild(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.mustDeploy()
	p, _ := h.st.Project(ctx, "app")
	must(t, h.st.SetEnv(ctx, p.ID, "API_KEY", "key-2"))
	if _, err := h.d.AddAddon(ctx, "app", "redis"); err != nil {
		t.Fatal(err)
	}

	var built atomic.Bool // the hook runs on every container goroutine
	h.rt.hook = func(method, _ string) error {
		if method == "Build" {
			built.Store(true)
		}
		return nil
	}
	var log bytes.Buffer
	rel, err := h.d.Redeploy(ctx, "app", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if built.Load() || rel.Seq != 2 {
		t.Errorf("built = %v, seq = %d", built.Load(), rel.Seq)
	}
	h.assertLive(2)
	web := h.rt.env("app_web_r2")
	if web["API_KEY"] != "key-2" || web["REDIS_URL"] == "" {
		t.Errorf("web env = %v", web)
	}
	// r2 has its own tag: pruning r1's never takes r2's image away.
	h.rt.mu.Lock()
	ok := h.rt.images[ImageName("app", 2)]
	h.rt.mu.Unlock()
	if !ok {
		t.Error("r2 has no image of its own")
	}
}

func TestRemoveAddon(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.d.AddAddon(ctx, "app", "redis"); err != nil {
		t.Fatal(err)
	}
	must(t, h.d.RemoveAddon(ctx, "app", "redis"))
	h.rt.mu.Lock()
	_, vol := h.rt.volumes[VolumeName("app", "redis")]
	h.rt.mu.Unlock()
	if slices.Contains(h.rt.names(), "app_redis") || vol {
		t.Error("container or data left after removal")
	}
	h.mustDeploy()
	if _, ok := h.rt.env("app_web_r1")["REDIS_URL"]; ok {
		t.Error("REDIS_URL still injected after removal")
	}
}

func TestFailuresAreReportedOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	if _, err := h.d.AddAddon(ctx, "app", "postgres"); err != nil {
		t.Fatal(err)
	}
	wantOne := func(what, sub string) {
		t.Helper()
		if len(h.alerts) != 1 || !strings.Contains(h.alerts[0], sub) {
			t.Fatalf("%s: alerts = %q, want one containing %q", what, h.alerts, sub)
		}
		h.alerts = nil
	}

	h.rt.hook = failOn("Exec", "app_postgres")
	if _, _, err := h.deploy(); err == nil {
		t.Fatal("deploy with a failing backup succeeded")
	}
	wantOne("backup before a release", "deploy r2 failed")

	if _, err := h.d.BackupNow(ctx, "app"); err == nil {
		t.Fatal("manual backup succeeded")
	}
	wantOne("manual backup", "backup failed")

	h.rt.hook = nil
	h.mustDeploy()
	h.rt.crashOnStart["app_web_r1"] = true
	if err := h.d.Rollback(ctx, "app", 1, io.Discard); err == nil {
		t.Fatal("rollback succeeded")
	}
	wantOne("rollback", "rollback to r1 failed")

	h.mustDeploy()
	if len(h.alerts) != 0 {
		t.Fatalf("a good deploy alerted: %q", h.alerts)
	}
}

func backupNames(t *testing.T, h *harness) []string {
	t.Helper()
	all, err := h.d.Backups("app")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range all {
		out = append(out, b.Name[strings.IndexByte(b.Name, '.')+1:])
	}
	slices.Sort(out)
	return out
}

func TestBackupNowCoversDatabasesAndVolumes(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	for _, k := range []string{"postgres", "redis"} {
		if _, err := h.d.AddAddon(ctx, "app", k); err != nil {
			t.Fatal(err)
		}
	}
	bs, err := h.d.BackupNow(ctx, "app")
	if err != nil || len(bs) != 3 {
		t.Fatalf("backups = %+v, %v", bs, err)
	}
	if got, want := backupNames(t, h), []string{"dump", "rdb", "vol-data.tar"}; !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for _, b := range bs {
		path, err := h.d.BackupPath("app", b.Name)
		if err != nil {
			t.Fatal(err)
		}
		if body, _ := os.ReadFile(path); len(body) == 0 {
			t.Errorf("%s is empty", b.Name)
		}
	}
	if len(h.rt.serviceNames()) != 2 || slices.Contains(h.rt.removed, "app_backup_data") == false {
		t.Errorf("the tar container was not cleaned up: removed %v", h.rt.removed)
	}
}

func TestOneFailingTargetDoesNotHideTheOthers(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	if _, err := h.d.AddAddon(ctx, "app", "redis"); err != nil {
		t.Fatal(err)
	}
	h.rt.hook = failOn("Run", "app_backup_data")
	bs, err := h.d.BackupNow(ctx, "app")
	if err == nil || len(bs) != 1 || !strings.HasSuffix(bs[0].Name, ".rdb") {
		t.Fatalf("backups = %+v, err = %v", bs, err)
	}
	if len(h.alerts) != 1 || !strings.Contains(h.alerts[0], "vol-data.tar") {
		t.Fatalf("alerts = %q", h.alerts)
	}
}

func TestRetentionIsPerKind(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	if _, err := h.d.AddAddon(ctx, "app", "redis"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.backups, "app")
	must(t, os.MkdirAll(dir, 0o700))
	for i := range 20 { // old: sort before anything made now
		ts := fmt.Sprintf("2020010%dT0000%02dZ", 1+i/10, i)
		must(t, os.WriteFile(filepath.Join(dir, ts+"-daily.rdb"), []byte("x"), 0o600))
	}
	if _, err := h.d.BackupNow(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, n := range backupNames(t, h) {
		counts[n]++
	}
	if counts["rdb"] != keepBackups || counts["vol-data.tar"] != 1 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestDailyBackupsOnlyRefreshStaleKinds(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	if _, err := h.d.AddAddon(ctx, "app", "redis"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.BackupNow(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	h.d.DailyBackups(ctx)
	if n := len(backupNames(t, h)); n != 2 {
		t.Fatalf("fresh backups were repeated: %d files", n)
	}
	all, _ := h.d.Backups("app")
	for _, b := range all {
		if strings.HasSuffix(b.Name, ".rdb") {
			old := time.Now().Add(-48 * time.Hour)
			must(t, os.Chtimes(filepath.Join(h.backups, "app", b.Name), old, old))
		}
	}
	h.d.DailyBackups(ctx)
	if got := backupNames(t, h); !slices.Equal(got, []string{"rdb", "rdb", "vol-data.tar"}) {
		t.Fatalf("kinds = %v", got)
	}
}

func TestKaylySnapshotCarriesDatabaseAndKey(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mustDeploy()
	b, err := h.d.SnapshotKayly(ctx, "manual")
	if err != nil || !strings.HasSuffix(b.Name, ".kayly.tar") {
		t.Fatalf("snapshot = %+v, %v", b, err)
	}
	f, err := os.Open(filepath.Join(h.backups, KaylyBackups, b.Name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := map[string]string{}
	for tr := tar.NewReader(f); ; {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(tr)
		got[hdr.Name] = string(body)
	}
	if got["master.key"] != "the-master-key" || !strings.HasPrefix(got["kayly.db"], "SQLite format 3") {
		t.Fatalf("archive holds %d entries; key %q", len(got), got["master.key"])
	}
	// No scratch file may linger where the backup list or the uploader could pick it up.
	entries, _ := os.ReadDir(filepath.Join(h.backups, KaylyBackups))
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the backup", len(entries))
	}
	h.d.DailyBackups(ctx)
	if all, _ := h.d.Backups(KaylyBackups); len(all) != 1 {
		t.Fatalf("a fresh snapshot was repeated: %d", len(all))
	}
}

// The entrypoints of both add-ons start as root and walk a data directory owned by their
// own user. On a fresh volume that works with fewer capabilities; once data exists (a
// recreated container) it needs DAC_OVERRIDE to read the 0700 directories.
func TestAddonsCanReadTheirOwnDataWhenRecreated(t *testing.T) {
	for kind := range addonKinds {
		spec := AddonSpec("app", store.Addon{Kind: kind, Image: "img", Password: "pw"})
		if !slices.Contains(spec.CapAdd, "DAC_OVERRIDE") {
			t.Errorf("%s add-on lacks DAC_OVERRIDE: %v", kind, spec.CapAdd)
		}
	}
}
