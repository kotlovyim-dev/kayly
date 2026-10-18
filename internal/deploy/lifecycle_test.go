package deploy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kotlovyim-dev/kayly/internal/store"
)

func TestRestartServiceRecreatesOnlyThatContainer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.mustDeploy()
	h.rt.mu.Lock()
	h.rt.removed = nil
	h.rt.mu.Unlock()

	must(t, h.d.RestartService(ctx, "app", "worker"))
	if !slices.Equal(h.rt.removed, []string{"app_worker_r1"}) {
		t.Errorf("removed = %v, want only the worker", h.rt.removed)
	}
	h.assertLive(1)

	if err := h.d.RestartService(ctx, "app", "nope"); err == nil {
		t.Error("restart of an unknown service succeeded")
	}
}

func TestDeleteProjectRemovesEverythingButBackups(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.d.AddAddon(ctx, "app", "redis"); err != nil {
		t.Fatal(err)
	}
	h.mustDeploy()
	h.mustDeploy()
	other, err := h.st.CreateProject(ctx, "other")
	must(t, err)
	if _, err := h.st.CreateRelease(ctx, other.ID, []byte("[services.web]"), store.Commit{}); err != nil {
		t.Fatal(err)
	}

	must(t, h.d.DeleteProject(ctx, "app"))

	if _, err := h.st.Project(ctx, "app"); err == nil {
		t.Error("project record left")
	}
	if names := h.rt.names(); len(names) != 0 {
		t.Errorf("containers left: %v", names)
	}
	h.rt.mu.Lock()
	defer h.rt.mu.Unlock()
	for _, v := range []string{VolumeName("app", "redis"), VolumeName("app", "data")} {
		if h.rt.volumes[v] {
			t.Errorf("volume %s left", v)
		}
	}
	for _, img := range []string{ImageName("app", 1), ImageName("app", 2)} {
		if h.rt.images[img] {
			t.Errorf("image %s left", img)
		}
	}
	if _, err := h.st.Project(ctx, "other"); err != nil {
		t.Errorf("another project was touched: %v", err)
	}
}

func TestReleaseNumberIsAnnouncedBeforeThePipelineRuns(t *testing.T) {
	h := newHarness(t)
	var got []int
	ctx := WithReleaseNotify(context.Background(), func(seq int) { got = append(got, seq) })

	must(t, os.WriteFile(filepath.Join(h.dir, "x"), nil, 0o644))
	var log bytes.Buffer
	if _, err := h.d.Deploy(ctx, "app", h.dir, store.Commit{}, &log); err != nil {
		t.Fatalf("deploy: %v\n%s", err, &log)
	}
	if _, err := h.d.Deploy(ctx, "app", h.dir, store.Commit{}, &log); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.Redeploy(ctx, "app", &log); err != nil {
		t.Fatal(err)
	}
	must(t, h.d.Rollback(ctx, "app", 2, &log))
	if !slices.Equal(got, []int{1, 2, 3, 2}) {
		t.Errorf("announced %v, want [1 2 3 2]", got)
	}
}
