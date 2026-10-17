package deploy

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

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

	built := false
	h.rt.hook = func(method, _ string) error {
		built = built || method == "Build"
		return nil
	}
	var log bytes.Buffer
	rel, err := h.d.Redeploy(ctx, "app", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if built || rel.Seq != 2 {
		t.Errorf("built = %v, seq = %d", built, rel.Seq)
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
