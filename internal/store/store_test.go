package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kotlovyim-dev/kayly/internal/secret"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kayly.db")
	box, _ := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	s, err := Open(context.Background(), path, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestReopenKeepsData(t *testing.T) {
	ctx := context.Background()
	s, path := open(t)
	if _, err := s.CreateProject(ctx, "leryo"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	box, _ := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	s2, err := Open(ctx, path, box)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Project(ctx, "leryo"); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseLifecycle(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "leryo")

	r1, err := s.CreateRelease(ctx, p.ID, []byte("[services.app]"))
	if err != nil || r1.Seq != 1 || r1.Status != StatusBuilding {
		t.Fatalf("r1 = %+v, %v", r1, err)
	}
	env := map[string]map[string]string{"app": {"SECRET": "s3cret"}}
	must(t, s.SetBuilt(ctx, r1.ID, "kayly/leryo:r1", env))
	must(t, s.SetStarting(ctx, r1.ID))
	must(t, s.Activate(ctx, p.ID, r1.ID))

	r2, _ := s.CreateRelease(ctx, p.ID, []byte("[services.app]"))
	if r2.Seq != 2 {
		t.Fatalf("r2.Seq = %d", r2.Seq)
	}
	if err := s.Activate(ctx, p.ID, r2.ID); err == nil {
		t.Fatal("activated a release that is still building")
	}
	must(t, s.SetBuilt(ctx, r2.ID, "kayly/leryo:r2", env))
	must(t, s.SetStarting(ctx, r2.ID))
	must(t, s.Activate(ctx, p.ID, r2.ID))

	p, _ = s.Project(ctx, "leryo")
	if p.ActiveRelease != r2.ID {
		t.Fatalf("active = %d, want %d", p.ActiveRelease, r2.ID)
	}
	old, _ := s.Release(ctx, p.ID, 1)
	if old.Status != StatusSuperseded || old.Env["app"]["SECRET"] != "s3cret" {
		t.Fatalf("r1 = %+v", old)
	}

	// Rollback: the superseded release goes live again.
	must(t, s.Activate(ctx, p.ID, r1.ID))
	if r, _ := s.Release(ctx, p.ID, 2); r.Status != StatusSuperseded {
		t.Errorf("r2 status = %s after rollback", r.Status)
	}

	// A failed release can never be activated or failed again.
	r3, _ := s.CreateRelease(ctx, p.ID, []byte("x"))
	must(t, s.Fail(ctx, r3.ID, "build failed"))
	if err := s.Activate(ctx, p.ID, r3.ID); err == nil {
		t.Error("activated a failed release")
	}
	if err := s.Fail(ctx, r1.ID, "x"); err == nil {
		t.Error("failed a live release")
	}
}

func TestFailInterrupted(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "ten00")
	live, _ := s.CreateRelease(ctx, p.ID, []byte("x"))
	must(t, s.SetBuilt(ctx, live.ID, "img", nil))
	must(t, s.SetStarting(ctx, live.ID))
	must(t, s.Activate(ctx, p.ID, live.ID))
	stuck, _ := s.CreateRelease(ctx, p.ID, []byte("x"))

	got, err := s.FailInterrupted(ctx)
	if err != nil || len(got) != 1 || got[0].ID != stuck.ID {
		t.Fatalf("FailInterrupted = %+v, %v", got, err)
	}
	if r, _ := s.ReleaseByID(ctx, live.ID); r.Status != StatusLive {
		t.Errorf("live release became %s", r.Status)
	}
}

func TestEnvIsEncrypted(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "leryo")
	must(t, s.SetEnv(ctx, p.ID, "TOKEN", "plain-secret-value"))
	must(t, s.SetEnv(ctx, p.ID, "TOKEN", "plain-secret-value-2"))
	env, err := s.Env(ctx, p.ID)
	if err != nil || len(env) != 1 || env["TOKEN"] != "plain-secret-value-2" {
		t.Fatalf("env = %v, %v", env, err)
	}
	var raw []byte
	must(t, s.db.QueryRowContext(ctx, "SELECT value FROM env_var").Scan(&raw))
	if bytes.Contains(raw, []byte("plain-secret")) {
		t.Fatal("env value stored in plaintext")
	}
	must(t, s.DeleteEnv(ctx, p.ID, "TOKEN"))
	if env, _ := s.Env(ctx, p.ID); len(env) != 0 {
		t.Errorf("env after delete = %v", env)
	}
}

func TestDomains(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	a, _ := s.CreateProject(ctx, "a")
	b, _ := s.CreateProject(ctx, "b")
	must(t, s.AddDomain(ctx, Domain{Hostname: "x.io", ProjectID: a.ID, Service: "web"}))
	if err := s.AddDomain(ctx, Domain{Hostname: "x.io", ProjectID: b.ID, Service: "web"}); err == nil {
		t.Error("one hostname added to two projects")
	}
	if ds, _ := s.Domains(ctx, a.ID); len(ds) != 1 || ds[0].Service != "web" {
		t.Errorf("domains = %+v", ds)
	}
	must(t, s.RemoveDomain(ctx, "x.io"))
	if err := s.RemoveDomain(ctx, "x.io"); err == nil {
		t.Error("removed a missing domain without error")
	}
}

func TestProjectNotFound(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Project(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
