package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/deploy"
)

// testImage is pulled before the tests run; pinned like everything Kayly runs.
const testImage = "alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8"

// newRuntime returns a Runtime for tests that need a real Docker daemon. They run only
// with KAYLY_DOCKER_TESTS=1, as in CI.
func newRuntime(t *testing.T) *Runtime {
	t.Helper()
	if os.Getenv("KAYLY_DOCKER_TESTS") != "1" {
		t.Skip("set KAYLY_DOCKER_TESTS=1 to run tests against Docker")
	}
	ctx := context.Background()
	if out, err := exec.CommandContext(ctx, "docker", "pull", "-q", testImage).CombinedOutput(); err != nil {
		t.Fatalf("pull %s: %v\n%s", testImage, err, out)
	}
	r, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// unique prefixes names so parallel or aborted runs never collide.
func unique(t *testing.T, name string) string {
	return fmt.Sprintf("kaylytest-%d-%s", time.Now().UnixNano(), name)
}

func TestServiceLifecycle(t *testing.T) {
	r := newRuntime(t)
	ctx := context.Background()
	net, vol, name := unique(t, "net"), unique(t, "vol"), unique(t, "svc")
	must(t, r.EnsureNetwork(ctx, net))
	must(t, r.EnsureNetwork(ctx, net)) // idempotent
	must(t, r.EnsureNetwork(ctx, deploy.EdgeNetwork))
	must(t, r.EnsureVolume(ctx, vol))
	t.Cleanup(func() {
		exec.Command("docker", "network", "rm", net).Run()
		exec.Command("docker", "volume", "rm", vol).Run()
	})

	spec := deploy.Spec{
		Name:     name,
		Image:    testImage,
		Cmd:      []string{"sh", "-c", `echo "hello $GREETING"; touch /data/ok; exec sleep 600`},
		Env:      map[string]string{"GREETING": "kayly"},
		Labels:   map[string]string{deploy.LabelProject: "test", deploy.LabelService: "svc"},
		Networks: []deploy.Network{{Name: net, Aliases: []string{"svc"}}, {Name: deploy.EdgeNetwork}},
		Mounts:   []deploy.Mount{{Volume: vol, Path: "/data"}},
		ReadOnly: true,
		Restart:  true,
	}
	must(t, r.Run(ctx, spec))
	t.Cleanup(func() { r.Remove(context.Background(), name, 0) })
	if err := r.Run(ctx, spec); err == nil {
		t.Error("Run with a taken name succeeded")
	}

	c, err := r.Inspect(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Running || c.IP == "" || c.Labels[deploy.LabelService] != "svc" || c.StartedAt.IsZero() {
		t.Errorf("inspect = %+v", c)
	}

	var logs bytes.Buffer
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(logs.String(), "hello kayly") && time.Now().Before(deadline) {
		logs.Reset()
		must(t, r.Logs(ctx, name, false, 10, &logs))
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "hello kayly") {
		t.Errorf("logs = %q", logs.String())
	}

	all, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(all, func(c deploy.Container) bool { return c.Name == name && c.Running }) {
		t.Errorf("List does not contain running %s", name)
	}

	must(t, r.Remove(ctx, name, time.Second))
	must(t, r.Remove(ctx, name, time.Second)) // missing is fine
	if _, err := r.Inspect(ctx, name); !errors.Is(err, deploy.ErrNoContainer) {
		t.Errorf("inspect after remove: %v", err)
	}
}

func TestOneOffExitCode(t *testing.T) {
	r := newRuntime(t)
	ctx := context.Background()
	name := unique(t, "release")
	must(t, r.Run(ctx, deploy.Spec{Name: name, Image: testImage, Cmd: []string{"sh", "-c", "echo migrating; exit 3"}}))
	t.Cleanup(func() { r.Remove(context.Background(), name, 0) })

	var logs bytes.Buffer
	must(t, r.Logs(ctx, name, true, 0, &logs)) // follow returns when the container exits
	code, err := r.Wait(ctx, name)
	if err != nil || code != 3 {
		t.Fatalf("Wait = %d, %v", code, err)
	}
	if logs.String() != "migrating\n" {
		t.Errorf("logs = %q", logs.String())
	}
}

func TestHardeningDefaults(t *testing.T) {
	r := newRuntime(t)
	ctx := context.Background()
	name := unique(t, "caps")
	// chown needs CAP_CHOWN, which every container loses.
	must(t, r.Run(ctx, deploy.Spec{Name: name, Image: testImage, Cmd: []string{"chown", "nobody", "/tmp"}}))
	t.Cleanup(func() { r.Remove(context.Background(), name, 0) })
	if code, err := r.Wait(ctx, name); err != nil || code == 0 {
		t.Fatalf("chown without capabilities: exit %d, %v", code, err)
	}
}

func TestImages(t *testing.T) {
	r := newRuntime(t)
	ctx := context.Background()
	if ok, err := r.ImageExists(ctx, testImage); err != nil || !ok {
		t.Fatalf("ImageExists(test image) = %v, %v", ok, err)
	}
	if ok, err := r.ImageExists(ctx, "kayly/does-not-exist:r1"); err != nil || ok {
		t.Fatalf("ImageExists(missing) = %v, %v", ok, err)
	}
	must(t, r.RemoveImage(ctx, "kayly/does-not-exist:r1"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
