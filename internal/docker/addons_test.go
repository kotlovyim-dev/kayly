package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// waitHealthy polls an add-on's Docker healthcheck.
func waitHealthy(t *testing.T, r *Runtime, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if c, err := r.Inspect(context.Background(), name); err == nil && c.Running && c.Health == "healthy" {
			return
		}
		time.Sleep(time.Second)
	}
	out, _ := exec.Command("docker", "logs", "--tail", "20", name).CombinedOutput()
	t.Fatalf("%s did not become healthy:\n%s", name, out)
}

// An upgrade recreates the add-on containers when their spec changed. The new container
// starts over a data directory the old one wrote, which a fresh volume never exercises: the
// image entrypoints walk it as root and the data belongs to their own user.
func TestAddonsStartOverExistingData(t *testing.T) {
	cases := []struct {
		kind        string
		write, read func(pw, db string) []string
	}{
		{
			kind: "redis",
			write: func(pw, _ string) []string {
				return []string{"valkey-cli", "--no-auth-warning", "-a", pw, "SET", "kept", "yes"}
			},
			read: func(pw, _ string) []string {
				return []string{"valkey-cli", "--no-auth-warning", "-a", pw, "GET", "kept"}
			},
		},
		{
			kind: "postgres",
			write: func(_, db string) []string {
				return []string{"psql", "-U", "postgres", "-d", db, "-c", "CREATE TABLE kept (x text); INSERT INTO kept VALUES ('yes')"}
			},
			read: func(_, db string) []string {
				return []string{"psql", "-U", "postgres", "-d", db, "-tAc", "SELECT x FROM kept"}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			r := newRuntime(t)
			ctx := context.Background()
			// The postgres add-on names its database after the project.
			project := fmt.Sprintf("kt-%d-%s", time.Now().UnixNano(), c.kind)
			db := strings.ReplaceAll(project, "-", "_")
			const pw = "pw-for-the-test"
			spec := deploy.AddonSpec(project, store.Addon{Kind: c.kind, Image: deploy.AddonImage(c.kind), Password: pw})
			must(t, r.EnsureNetwork(ctx, spec.Networks[0].Name))
			t.Cleanup(func() {
				exec.Command("docker", "rm", "-f", spec.Name).Run()
				exec.Command("docker", "volume", "rm", spec.Mounts[0].Volume).Run()
				exec.Command("docker", "network", "rm", spec.Networks[0].Name).Run()
			})

			must(t, r.EnsureContainer(ctx, spec))
			waitHealthy(t, r, spec.Name)
			var out bytes.Buffer
			if code, err := r.Exec(ctx, spec.Name, c.write(pw, db), nil, &out); err != nil || code != 0 {
				t.Fatalf("write: %d %v\n%s", code, err, &out)
			}

			// What an upgrade does: the container goes, the volume stays, a new one starts.
			must(t, r.Remove(ctx, spec.Name, 30*time.Second))
			must(t, r.EnsureContainer(ctx, spec))
			waitHealthy(t, r, spec.Name)

			out.Reset()
			if code, err := r.Exec(ctx, spec.Name, c.read(pw, db), nil, &out); err != nil || code != 0 || strings.TrimSpace(out.String()) != "yes" {
				t.Fatalf("read after recreation: %d %v %q, want yes", code, err, out.String())
			}
		})
	}
}
