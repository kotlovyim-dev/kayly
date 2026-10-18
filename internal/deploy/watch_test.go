package deploy

import (
	"context"
	"strings"
	"testing"
	"time"
)

// watchHarness is a deployed app with a clock the test controls.
func watchHarness(t *testing.T) (*harness, *time.Time) {
	t.Helper()
	h := newHarness(t)
	h.mustDeploy()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	h.d.clock = func() time.Time { return now }
	h.alerts = nil
	h.d.Watch(context.Background()) // the baseline look
	return h, &now
}

func TestRestartLoopIsReportedOnceAndWhenItEnds(t *testing.T) {
	ctx := context.Background()
	h, now := watchHarness(t)
	h.rt.setRestarts("app_web_r1", 1)
	h.d.Watch(ctx)
	h.rt.setRestarts("app_web_r1", 2)
	h.d.Watch(ctx)
	if len(h.alerts) != 0 {
		t.Fatalf("two restarts are not a loop: %q", h.alerts)
	}
	h.rt.setRestarts("app_web_r1", 3)
	h.d.Watch(ctx)
	if len(h.alerts) != 1 || !strings.Contains(h.alerts[0], "web keeps restarting") || !strings.Contains(h.alerts[0], "[app_web_r1 output]") {
		t.Fatalf("alerts = %q, want one with the log tail", h.alerts)
	}
	h.rt.setRestarts("app_web_r1", 5)
	h.d.Watch(ctx)
	*now = now.Add(5 * time.Minute)
	h.d.Watch(ctx)
	if len(h.alerts) != 1 {
		t.Fatalf("a loop that goes on was reported again: %q", h.alerts)
	}
	*now = now.Add(loopWindow + time.Minute)
	h.d.Watch(ctx)
	if len(h.alerts) != 2 || !strings.Contains(h.alerts[1], "stable again") {
		t.Fatalf("alerts = %q, want a recovery", h.alerts)
	}
	h.d.Watch(ctx)
	if len(h.alerts) != 2 {
		t.Fatalf("recovery repeated: %q", h.alerts)
	}
}

func TestRestartsFromBeforeTheDaemonStartedAreHistory(t *testing.T) {
	h := newHarness(t)
	h.mustDeploy()
	h.rt.setRestarts("app_web_r1", 40)
	h.alerts = nil
	h.d.Watch(context.Background())
	h.d.Watch(context.Background())
	if len(h.alerts) != 0 {
		t.Fatalf("alerts = %q", h.alerts)
	}
}

func TestFailingHealthCheckOfALiveServiceIsReported(t *testing.T) {
	ctx := context.Background()
	h, _ := watchHarness(t)
	h.rt.mu.Lock()
	h.rt.unhealthy["app_web_r1"] = true
	h.rt.mu.Unlock()
	for range probeFails - 1 {
		h.d.Watch(ctx)
	}
	if len(h.alerts) != 0 {
		t.Fatalf("one blip is not an outage: %q", h.alerts)
	}
	h.d.Watch(ctx)
	h.d.Watch(ctx)
	if len(h.alerts) != 1 || !strings.Contains(h.alerts[0], "fails its health check") {
		t.Fatalf("alerts = %q", h.alerts)
	}
	h.rt.mu.Lock()
	delete(h.rt.unhealthy, "app_web_r1")
	h.rt.mu.Unlock()
	h.d.Watch(ctx)
	if len(h.alerts) != 2 || !strings.Contains(h.alerts[1], "passes its health check again") {
		t.Fatalf("alerts = %q", h.alerts)
	}
}

func TestWatchLeavesAProjectAloneWhileItIsBusy(t *testing.T) {
	ctx := context.Background()
	h, _ := watchHarness(t)
	h.rt.mu.Lock()
	h.rt.unhealthy["app_web_r1"] = true
	h.rt.mu.Unlock()
	l := h.d.projectLock("app")
	l.Lock() // a deploy is running
	for range 2 * probeFails {
		h.d.Watch(ctx)
	}
	l.Unlock()
	if len(h.alerts) != 0 {
		t.Fatalf("alerts during a deploy: %q", h.alerts)
	}
}

func TestStateOfAReplacedReleaseIsForgotten(t *testing.T) {
	ctx := context.Background()
	h, _ := watchHarness(t)
	h.rt.setRestarts("app_web_r1", 2)
	h.d.Watch(ctx)
	h.mustDeploy() // r2 replaces r1
	h.d.Watch(ctx)
	h.d.watch.mu.Lock()
	_, stale := h.d.watch.svc["app_web_r1"]
	h.d.watch.mu.Unlock()
	if stale {
		t.Fatal("state of r1 is kept after r2 went live")
	}
}

func TestDiskAlertHasHysteresis(t *testing.T) {
	ctx := context.Background()
	h, _ := watchHarness(t)
	h.d.opt.DiskPath = "/data"
	pct := uint64(50)
	h.d.diskUsage = func(string) (uint64, uint64, error) { return pct, 100, nil }
	steps := []struct {
		pct    uint64
		alerts int
		match  string
	}{
		{50, 0, ""},
		{85, 1, "85% full"},
		{90, 1, ""},            // still full: no repeat
		{82, 1, ""},            // below the alert but not below the clear level
		{79, 2, "down to 79%"}, // recovered
		{70, 2, ""},            // and silent
		{86, 3, "86% full"},    // and it can happen again
	}
	for _, s := range steps {
		pct = s.pct
		h.d.Watch(ctx)
		if len(h.alerts) != s.alerts || (s.match != "" && !strings.Contains(h.alerts[len(h.alerts)-1], s.match)) {
			t.Fatalf("at %d%%: alerts = %q", s.pct, h.alerts)
		}
	}
}
