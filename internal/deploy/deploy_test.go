package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/proxy"
	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// app has a release step, a web service with a volume and a worker, and env of every kind.
const app = `
[release]
command = "node migrate.ts"

[services.web]
port = 3000
health = "/health"
volumes = ["data:/data"]

[services.worker]
command = "node worker.ts --queue 'default jobs'"

[env]
SECRET = { generate = "hex:16" }
API_KEY = {}
ADMIN = { to = ["release"] }
ORIGIN = { template = "${web.url}" }
`

type harness struct {
	t   *testing.T
	st  *store.Store
	rt  *fakeRuntime
	px  *fakeProxy
	d   *Deployer
	dir string
	// backups is the Deployer's BackupDir.
	backups string
	keyFile string
	// alerts are the notifications the Deployer sent.
	alerts []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	box, _ := secret.NewBox(bytes.Repeat([]byte{1}, 32))
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kayly.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := st.CreateProject(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	must(t, st.SetEnv(ctx, p.ID, "API_KEY", "key-1"))
	must(t, st.SetEnv(ctx, p.ID, "ADMIN", "root"))

	h := &harness{t: t, st: st, rt: newFakeRuntime(), px: &fakeProxy{}, dir: t.TempDir(), backups: t.TempDir()}
	h.writeConfig(app)
	h.keyFile = filepath.Join(t.TempDir(), "master.key")
	must(t, os.WriteFile(h.keyFile, []byte("the-master-key"), 0o600))
	h.d = h.newDeployer()
	return h
}

// newDeployer returns a fresh Deployer over the same store, runtime and proxy, as a
// restarted daemon would have.
func (h *harness) newDeployer() *Deployer {
	d := New(h.st, h.rt, h.rt, h.px, Options{
		BaseDomain:    "1-2-3-4.sslip.io",
		Proxy:         proxy.Options{AdminListen: "unix//run/caddy.sock"},
		HealthTimeout: 300 * time.Millisecond,
		KeepImages:    2,
		BackupDir:     h.backups,
		KeyFile:       h.keyFile,
		Notify:        func(_ context.Context, text string) { h.alerts = append(h.alerts, text) },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.checkHealth = h.rt.health
	d.settle = 0
	return d
}

func (h *harness) writeConfig(toml string) {
	h.t.Helper()
	must(h.t, os.WriteFile(filepath.Join(h.dir, "kayly.toml"), []byte(toml), 0o644))
}

func (h *harness) deploy() (store.Release, string, error) {
	var log bytes.Buffer
	rel, err := h.d.Deploy(context.Background(), "app", h.dir, &log)
	return rel, log.String(), err
}

func (h *harness) mustDeploy() store.Release {
	h.t.Helper()
	rel, log, err := h.deploy()
	if err != nil {
		h.t.Fatalf("deploy: %v\n%s", err, log)
	}
	return rel
}

// assertLive checks the whole observable state against "release seq is live": the
// database, the running containers and the proxy.
func (h *harness) assertLive(seq int) {
	h.t.Helper()
	ctx := context.Background()
	p, _ := h.st.Project(ctx, "app")
	rel, err := h.st.ReleaseByID(ctx, p.ActiveRelease)
	if err != nil || rel.Seq != seq || rel.Status != store.StatusLive {
		h.t.Errorf("active release = r%d (%s), want r%d live (err: %v)", rel.Seq, rel.Status, seq, err)
	}
	want := []string{ContainerName("app", "web", seq), ContainerName("app", "worker", seq)}
	if got := h.rt.serviceNames(); !slices.Equal(got, want) {
		h.t.Errorf("containers = %v, want %v", got, want)
	}
	if !h.px.routesTo(ContainerName("app", "web", seq) + ":3000") {
		h.t.Errorf("proxy does not route to r%d:\n%s", seq, h.px.last)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFirstDeploy(t *testing.T) {
	h := newHarness(t)
	rel := h.mustDeploy()
	if rel.Seq != 1 || rel.Status != store.StatusLive {
		t.Fatalf("release = r%d %s", rel.Seq, rel.Status)
	}
	h.assertLive(1)

	web := h.rt.env("app_web_r1")
	if web["API_KEY"] != "key-1" || len(web["SECRET"]) != 32 || web["ORIGIN"] != "https://web.app.1-2-3-4.sslip.io" {
		t.Errorf("web env = %v", web)
	}
	if _, ok := web["ADMIN"]; ok {
		t.Error("ADMIN reached the web service; it is scoped to the release step")
	}
	if !h.px.routesTo("app_web_r1:3000") || !strings.Contains(h.px.last, "web.app.1-2-3-4.sslip.io") {
		t.Errorf("proxy config:\n%s", h.px.last)
	}
	h.rt.mu.Lock()
	worker := h.rt.containers["app_worker_r1"].spec
	h.rt.mu.Unlock()
	if !slices.Equal(worker.Cmd, []string{"node", "worker.ts", "--queue", "default jobs"}) {
		t.Errorf("worker cmd = %q", worker.Cmd)
	}
	if slices.ContainsFunc(worker.Networks, func(n Network) bool { return n.Name == EdgeNetwork }) {
		t.Error("worker has no port but is on the edge network")
	}
}

func TestGeneratedSecretIsStable(t *testing.T) {
	h := newHarness(t)
	h.mustDeploy()
	first := h.rt.env("app_web_r1")["SECRET"]
	h.mustDeploy()
	if got := h.rt.env("app_web_r2")["SECRET"]; got != first {
		t.Errorf("SECRET changed between deploys: %q → %q", first, got)
	}
}

func TestSecondDeployDrainsFirst(t *testing.T) {
	h := newHarness(t)
	h.mustDeploy()
	h.mustDeploy()
	h.assertLive(2)
	p, _ := h.st.Project(context.Background(), "app")
	if r1, _ := h.st.Release(context.Background(), p.ID, 1); r1.Status != store.StatusSuperseded {
		t.Errorf("r1 = %s", r1.Status)
	}
}

// Every way a deploy can fail must leave r1 serving, untouched.
func TestFailedDeployKeepsLiveRelease(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
		want  string
	}{
		{"build fails", func(h *harness) {
			h.rt.hook = failOn("Build", "kayly/app:r2")
		}, "build"},
		{"bad config", func(h *harness) {
			h.writeConfig("[services.web]\ncolour = 1\n")
		}, "unknown key"},
		{"missing env", func(h *harness) {
			h.writeConfig(app + "NEW_KEY = {}\n")
		}, "no value for NEW_KEY"},
		{"release step exits 1", func(h *harness) {
			h.rt.exitCode["app_release_r2"] = 1
		}, "release step: exited with code 1"},
		{"web crashes on start", func(h *harness) {
			h.rt.crashOnStart["app_web_r2"] = true
		}, "web crashed on start"},
		{"worker crashes on start", func(h *harness) {
			h.rt.crashOnStart["app_worker_r2"] = true
		}, "worker crashed on start"},
		{"web never healthy", func(h *harness) {
			h.rt.unhealthy["app_web_r2"] = true
		}, "web not healthy after 300ms: 503"},
		{"start fails", func(h *harness) {
			h.rt.hook = failOn("Run", "app_web_r2")
		}, "start web"},
		{"proxy rejects config", func(h *harness) {
			h.px.hook = func() error { return errors.New("caddy said no") }
		}, "caddy said no"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.mustDeploy()
			tt.setup(h)

			_, log, err := h.deploy()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tt.want, log)
			}
			h.rt.hook, h.px.hook = nil, nil
			h.assertLive(1)
			for _, name := range []string{"app_web_r1", "app_worker_r1"} {
				if h.rt.wasRemoved(name) {
					t.Errorf("%s was removed by a failed deploy", name)
				}
			}
			p, _ := h.st.Project(context.Background(), "app")
			if r2, err := h.st.Release(context.Background(), p.ID, 2); err == nil && r2.Status != store.StatusFailed {
				t.Errorf("r2 = %s, want failed", r2.Status)
			}
		})
	}
}

func TestCrashLogShownOnFailure(t *testing.T) {
	h := newHarness(t)
	h.rt.crashOnStart["app_web_r1"] = true
	_, log, err := h.deploy()
	if err == nil || !strings.Contains(log, "[app_web_r1 output]") {
		t.Fatalf("crash output missing from the deploy log (err %v):\n%s", err, log)
	}
}

func TestOverlapFalseStopsOldFirstAndRestoresOnFailure(t *testing.T) {
	h := newHarness(t)
	noOverlap := strings.Replace(app, "[services.worker]", "[services.worker]\noverlap = false", 1)
	h.writeConfig(noOverlap)
	h.mustDeploy()

	var order []string
	var mu sync.Mutex
	h.rt.hook = func(method, arg string) error {
		if method == "Run" || method == "Remove" {
			mu.Lock()
			order = append(order, method+" "+arg)
			mu.Unlock()
		}
		return nil
	}
	h.mustDeploy()
	stop, start := slices.Index(order, "Remove app_worker_r1"), slices.Index(order, "Run app_worker_r2")
	if stop < 0 || start < 0 || stop > start {
		t.Errorf("worker r1 must stop before worker r2 starts: %v", order)
	}
	h.assertLive(2)

	// If the new release fails after the old worker was stopped, the old worker comes back.
	h.rt.hook = nil
	h.rt.crashOnStart["app_worker_r3"] = true
	if _, _, err := h.deploy(); err == nil {
		t.Fatal("deploy of a crashing worker succeeded")
	}
	h.assertLive(2)
}

func TestRollback(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.mustDeploy()
	h.mustDeploy()

	var log bytes.Buffer
	if err := h.d.Rollback(ctx, "app", 1, &log); err != nil {
		t.Fatalf("rollback: %v\n%s", err, log.String())
	}
	h.assertLive(1)
	if strings.Contains(log.String(), "==> build") || strings.Contains(log.String(), "release:") {
		t.Errorf("rollback rebuilt or ran the release step:\n%s", log.String())
	}

	if err := h.d.Rollback(ctx, "app", 1, io.Discard); err == nil || !strings.Contains(err.Error(), "already live") {
		t.Errorf("rollback to the live release: %v", err)
	}
	h.rt.crashOnStart["app_web_r3"] = true
	h.deploy()
	if err := h.d.Rollback(ctx, "app", 3, io.Discard); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Errorf("rollback to a failed release: %v", err)
	}
}

func TestRollbackFailureKeepsLiveRelease(t *testing.T) {
	h := newHarness(t)
	h.mustDeploy()
	h.mustDeploy()
	h.rt.crashOnStart["app_web_r1"] = true
	if err := h.d.Rollback(context.Background(), "app", 1, io.Discard); err == nil {
		t.Fatal("rollback to a crashing release succeeded")
	}
	h.assertLive(2)
}

func TestPruneKeepsRecentImages(t *testing.T) {
	h := newHarness(t)
	for range 4 {
		h.mustDeploy()
	}
	ctx := context.Background()
	p, _ := h.st.Project(ctx, "app")
	for seq, want := range map[int]bool{1: false, 2: false, 3: true, 4: false} { // r4 is live, not a rollback target
		r, _ := h.st.Release(ctx, p.ID, seq)
		if got := h.d.CanRollBack(ctx, r); got != want {
			t.Errorf("CanRollBack(r%d) = %v, want %v", seq, got, want)
		}
	}
	h.rt.mu.Lock()
	defer h.rt.mu.Unlock()
	for seq, want := range map[int]bool{1: false, 2: false, 3: true, 4: true} {
		if got := h.rt.images[ImageName("app", seq)]; got != want {
			t.Errorf("image r%d kept = %v, want %v", seq, got, want)
		}
	}
}

// A daemon crash at any step of a deploy must converge, after a restart, to the last
// committed live release.
func TestCrashRecovery(t *testing.T) {
	tests := []struct {
		name     string
		method   string // runtime method, or "proxy-before" / "proxy-after" for the load
		arg      string
		wantLive int
	}{
		{"during build", "Build", "kayly/app:r2", 1},
		{"during release step", "Wait", "app_release_r2", 1},
		{"while starting", "Run", "app_web_r2", 1},
		{"during health check", "Inspect", "app_web_r2", 1},
		{"before proxy load", "proxy-before", "", 1},
		{"after proxy load, before commit", "proxy-after", "", 1},
		{"during drain", "Remove", "app_web_r1", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.mustDeploy()

			reached, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			block := func() error {
				once.Do(func() { close(reached) })
				<-release
				return errors.New("daemon is gone")
			}
			switch tt.method {
			case "proxy-before":
				h.px.hook = block
			case "proxy-after":
				h.px.hook = func() error {
					// Caddy took the config, then the daemon died before the commit.
					h.px.mu.Lock()
					h.px.last = loadedConfig(t, h, 2)
					h.px.mu.Unlock()
					return block()
				}
			default:
				h.rt.hook = func(method, arg string) error {
					if method == tt.method && arg == tt.arg {
						return block()
					}
					return nil
				}
			}

			done := make(chan struct{})
			go func() { defer close(done); h.deploy() }()
			<-reached
			// The zombie deploy is frozen; a new daemon starts over the same state.
			h.rt.mu.Lock()
			h.rt.hook = nil
			h.rt.mu.Unlock()
			h.px.mu.Lock()
			h.px.hook = nil
			h.px.mu.Unlock()

			restarted := h.newDeployer()
			if err := restarted.Recover(context.Background()); err != nil {
				t.Fatalf("recover: %v", err)
			}
			if err := restarted.Reconcile(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			h.assertLive(tt.wantLive)

			close(release)
			<-done
		})
	}
}

// loadedConfig is the proxy config that routes app to release seq.
func loadedConfig(t *testing.T, h *harness, seq int) string {
	cfg, err := proxy.Config([]proxy.Route{{Host: "web.app.1-2-3-4.sslip.io", Upstream: ContainerName("app", "web", seq) + ":3000"}},
		proxy.Options{AdminListen: "unix//run/caddy.sock"})
	if err != nil {
		t.Fatal(err)
	}
	return string(cfg)
}

func TestReconcileRepairsDrift(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.mustDeploy()

	// Drift: a live container vanished, a stray container of the project appeared, and a
	// container of a project that no longer exists is still around.
	h.rt.mu.Lock()
	delete(h.rt.containers, "app_worker_r1")
	h.rt.containers["app_web_r9"] = &fakeContainer{spec: Spec{Labels: labels("app", 9, RoleService, "web")}, running: true}
	h.rt.containers["gone_web_r1"] = &fakeContainer{spec: Spec{Labels: labels("gone", 1, RoleService, "web")}, running: true}
	h.rt.mu.Unlock()

	if err := h.d.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	h.assertLive(1)
}

func TestReconcileSkipsBusyProject(t *testing.T) {
	h := newHarness(t)
	h.mustDeploy()
	l := h.d.projectLock("app")
	l.Lock()
	h.rt.mu.Lock()
	delete(h.rt.containers, "app_worker_r1")
	h.rt.mu.Unlock()
	if err := h.d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(h.rt.names(), "app_worker_r1") {
		t.Error("reconcile touched a project locked by a deploy")
	}
	if h.rt.wasRemoved("app_web_r1") {
		t.Error("reconcile removed a live container of a project locked by a deploy")
	}
	l.Unlock()
}

// failOn returns a runtime hook that fails one call.
func failOn(method, arg string) func(string, string) error {
	return func(m, a string) error {
		if m == method && a == arg {
			return errors.New(m + " failed")
		}
		return nil
	}
}

func TestMissingEnvFailsBeforeBuild(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(app + "NEW_KEY = {}\n")
	built := false
	h.rt.hook = func(method, _ string) error {
		built = built || method == "Build"
		return nil
	}
	if _, _, err := h.deploy(); err == nil || built {
		t.Fatalf("err = %v, built = %v: a missing value must fail before the build", err, built)
	}
}

// A service with host ports is replaced, not overlapped: its old container frees the ports
// before the new one binds them.
func TestHostPortsReplaceInsteadOfOverlap(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(strings.Replace(app, "[services.worker]", "[services.worker]\nports = [\"7882/udp\"]", 1))
	h.mustDeploy()
	h.rt.mu.Lock()
	got := h.rt.containers["app_worker_r1"].spec.Ports
	h.rt.mu.Unlock()
	if len(got) != 1 || got[0] != (Port{Host: 7882, Container: 7882, Proto: "udp"}) {
		t.Fatalf("ports = %+v", got)
	}

	var order []string
	h.rt.hook = func(method, arg string) error {
		if (method == "Run" || method == "Remove") && strings.Contains(arg, "worker") {
			order = append(order, method+" "+arg)
		}
		return nil
	}
	h.mustDeploy()
	if !slices.Equal(order[:2], []string{"Remove app_worker_r1", "Run app_worker_r2"}) {
		t.Errorf("order = %v, want the old worker removed before the new one starts", order)
	}
	h.assertLive(2)
}
