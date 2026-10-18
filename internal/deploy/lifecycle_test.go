package deploy

import (
	"context"
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
