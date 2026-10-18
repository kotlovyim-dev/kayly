package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestDrillRestoresTheNewestOffsiteDumpIntoAThrowawayContainer(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.opt.Offsite = &fakeOffsite{files: map[string]map[string]string{"app": {
		"20260101T000000Z-daily.dump":  "old",
		"20260102T000000Z-daily.dump":  "newest",
		"20260103T000000Z-daily.rdb":   "not a dump",
		"20260103T000000Z-daily.kayly": "ignored",
	}}}
	h.rt.execs = nil
	if err := h.d.Drill(ctx); err != nil {
		t.Fatal(err)
	}
	var restore *execRecord
	for i := range h.rt.execs {
		if h.rt.execs[i].cmd[0] == "pg_restore" {
			restore = &h.rt.execs[i]
		}
	}
	if restore == nil || restore.container != "kayly_drill_app" || restore.input != "newest" {
		t.Fatalf("execs = %+v", h.rt.execs)
	}
	// The real database was not touched, and nothing of the drill is left behind.
	for _, e := range h.rt.execs {
		if e.container == "app_postgres" {
			t.Errorf("the live add-on was used: %+v", e)
		}
	}
	if !slices.Contains(h.rt.removed, "kayly_drill_app") || h.rt.volumes["kayly_drill_app"] {
		t.Errorf("drill container or volume left behind (removed %v)", h.rt.removed)
	}
	if len(h.alerts) != 0 {
		t.Errorf("alerts = %q", h.alerts)
	}
}

func TestFailedDrillAlertsAndCleansUp(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.opt.Offsite = &fakeOffsite{files: map[string]map[string]string{"app": {"20260102T000000Z-daily.dump": "bad"}}}
	h.rt.hook = func(method, arg string) error {
		if method == "Exec" && arg == "kayly_drill_app" {
			return errors.New("pg_restore: unsupported version")
		}
		return nil
	}
	if err := h.d.Drill(ctx); err == nil {
		t.Fatal("drill passed")
	}
	if len(h.alerts) != 1 || !strings.Contains(h.alerts[0], "restore drill failed") {
		t.Fatalf("alerts = %q", h.alerts)
	}
	if h.rt.volumes["kayly_drill_app"] {
		t.Error("drill volume left behind")
	}
}

func TestDrillWithoutADumpOffsiteYetIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.opt.Offsite = &fakeOffsite{files: map[string]map[string]string{}}
	if err := h.d.Drill(context.Background()); err != nil || len(h.alerts) != 0 {
		t.Fatalf("err = %v, alerts = %q", err, h.alerts)
	}
}

func TestDrillRunsWeekly(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.withAddons("postgres")
	h.d.opt.Offsite = &fakeOffsite{files: map[string]map[string]string{"app": {"20260102T000000Z-daily.dump": "x"}}}
	count := func() (n int) {
		for _, e := range h.rt.execs {
			if e.cmd[0] == "pg_restore" {
				n++
			}
		}
		return
	}
	h.rt.execs = nil
	h.d.DrillIfDue(ctx)
	h.d.DrillIfDue(ctx)
	if count() != 1 {
		t.Fatalf("drills = %d, want one within a week", count())
	}
	must(t, h.st.SetSetting(ctx, "drill_last", "2020-01-01T00:00:00Z"))
	h.d.DrillIfDue(ctx)
	if count() != 2 {
		t.Fatalf("drills = %d, want another after a week", count())
	}
}
