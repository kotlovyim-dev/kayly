package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestSetAsideMovesTheDatabaseAndItsLog(t *testing.T) {
	s, path := open(t)
	if _, err := s.CreateProject(context.Background(), "leryo"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	dir := filepath.Join(filepath.Dir(path), "aside")
	if err := SetAside(path, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the database is still in place: %v", err)
	}
	box, _ := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	moved, err := Open(context.Background(), filepath.Join(dir, "kayly.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	if ps, err := moved.Projects(context.Background()); err != nil || len(ps) != 1 {
		t.Fatalf("projects in the moved database = %v, %v", ps, err)
	}
	if err := SetAside(filepath.Join(dir, "missing.db"), dir); err == nil {
		t.Fatal("set aside a database that does not exist")
	}
}

func TestReleaseLifecycle(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "leryo")

	r1, err := s.CreateRelease(ctx, p.ID, []byte("[services.app]"), Commit{})
	if err != nil || r1.Seq != 1 || r1.Status != StatusBuilding {
		t.Fatalf("r1 = %+v, %v", r1, err)
	}
	env := map[string]map[string]string{"app": {"SECRET": "s3cret"}}
	must(t, s.SetBuilt(ctx, r1.ID, "kayly/leryo:r1", env))
	must(t, s.SetStarting(ctx, r1.ID))
	must(t, s.Activate(ctx, p.ID, r1.ID))

	r2, _ := s.CreateRelease(ctx, p.ID, []byte("[services.app]"), Commit{})
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
	r3, _ := s.CreateRelease(ctx, p.ID, []byte("x"), Commit{})
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
	live, _ := s.CreateRelease(ctx, p.ID, []byte("x"), Commit{})
	must(t, s.SetBuilt(ctx, live.ID, "img", nil))
	must(t, s.SetStarting(ctx, live.ID))
	must(t, s.Activate(ctx, p.ID, live.ID))
	stuck, _ := s.CreateRelease(ctx, p.ID, []byte("x"), Commit{})

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

func TestTokens(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "app")
	tok, err := s.CreateToken(ctx, NewToken{Name: "laptop", Scope: ScopeAdmin})
	if err != nil || len(tok) < 40 {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if _, err := s.LookupToken(ctx, tok); err != nil {
		t.Error("valid token rejected")
	}
	if _, err := s.LookupToken(ctx, tok+"x"); !errors.Is(err, ErrNotFound) {
		t.Error("wrong token accepted")
	}
	var raw []byte
	must(t, s.db.QueryRowContext(ctx, "SELECT hash FROM token").Scan(&raw))
	if bytes.Contains(raw, []byte(tok)) {
		t.Error("token stored in plaintext")
	}

	agent, _ := s.CreateToken(ctx, NewToken{Name: "agent", Scope: ScopeDeploy, ProjectID: p.ID, ExpiresAt: time.Now().Add(time.Hour)})
	got, err := s.LookupToken(ctx, agent)
	if err != nil || got.Scope != ScopeDeploy || got.Project != "app" || got.ExpiresAt.IsZero() {
		t.Fatalf("agent token = %+v, %v", got, err)
	}
	if !got.Allows(ScopeRead) || !got.Allows(ScopeDeploy) || got.Allows(ScopeAdmin) {
		t.Errorf("deploy token allows wrong scopes: %+v", got)
	}
	if _, err := s.CreateToken(ctx, NewToken{Name: "x", Scope: "root"}); err == nil {
		t.Error("unknown scope accepted")
	}

	expired, _ := s.CreateToken(ctx, NewToken{Name: "old", Scope: ScopeRead, ExpiresAt: time.Now().Add(-time.Second)})
	if _, err := s.LookupToken(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Error("expired token accepted")
	}

	list, _ := s.Tokens(ctx)
	must(t, s.DeleteToken(ctx, list[0].ID))
	if _, err := s.LookupToken(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Error("revoked token accepted")
	}
}

func TestAudit(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	must(t, s.Audit(ctx, AuditEntry{Token: "agent", Action: "POST /api/v1/projects/{project}/rollback", Project: "app"}))
	must(t, s.Audit(ctx, AuditEntry{Token: "local", Action: "POST /api/v1/projects"}))
	log, err := s.AuditLog(ctx, 10)
	if err != nil || len(log) != 2 || log[0].Token != "local" || log[1].Project != "app" {
		t.Fatalf("audit log = %+v, %v", log, err)
	}
}

func TestAddons(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "app")
	must(t, s.AddAddon(ctx, Addon{ProjectID: p.ID, Kind: "redis", Image: "valkey", Password: "pw-redis"}))
	must(t, s.AddAddon(ctx, Addon{ProjectID: p.ID, Kind: "postgres", Image: "postgres", Password: "pw-pg"}))
	if err := s.AddAddon(ctx, Addon{ProjectID: p.ID, Kind: "redis", Image: "valkey", Password: "x"}); err == nil {
		t.Error("a second redis was attached")
	}
	as, err := s.Addons(ctx, p.ID)
	if err != nil || len(as) != 2 || as[0].Kind != "postgres" || as[0].Password != "pw-pg" {
		t.Fatalf("addons = %+v, %v", as, err)
	}
	var raw []byte
	must(t, s.db.QueryRowContext(ctx, "SELECT password FROM addon WHERE kind = 'redis'").Scan(&raw))
	if bytes.Contains(raw, []byte("pw-redis")) {
		t.Error("add-on password stored in plaintext")
	}
	must(t, s.RemoveAddon(ctx, p.ID, "redis"))
	if err := s.RemoveAddon(ctx, p.ID, "redis"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove: %v", err)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if v, err := s.Setting(ctx, "k"); err != nil || v != "" {
		t.Fatalf("unset = %q, %v", v, err)
	}
	for _, v := range []string{"one", "two"} {
		if err := s.SetSetting(ctx, "k", v); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := s.Setting(ctx, "k"); v != "two" {
		t.Fatalf("got %q", v)
	}
	var raw []byte
	if err := s.db.QueryRow("SELECT value FROM setting").Scan(&raw); err != nil || bytes.Contains(raw, []byte("two")) {
		t.Fatalf("value stored in the clear: %v", err)
	}
	if err := s.SetSetting(ctx, "k", ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Setting(ctx, "k"); v != "" {
		t.Fatalf("not removed: %q", v)
	}
}

func TestReleaseKeepsItsCommit(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "app")
	want := Commit{SHA: "3a77de6-dirty", Message: "feat: Ünïcode subject"}
	r, err := s.CreateRelease(ctx, p.ID, []byte("x"), want)
	if err != nil || r.Commit != want {
		t.Fatalf("created = %+v, %v", r.Commit, err)
	}
	got, _ := s.Release(ctx, p.ID, r.Seq)
	if got.Commit != want {
		t.Fatalf("read back %+v", got.Commit)
	}
	if plain, _ := s.CreateRelease(ctx, p.ID, []byte("x"), Commit{}); plain.Commit != (Commit{}) {
		t.Fatalf("a release without a commit has %+v", plain.Commit)
	}
}

func TestSource(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p, _ := s.CreateProject(ctx, "app")
	if src, err := s.Source(ctx, p.ID); err != nil || src.URL != "" {
		t.Fatalf("new project follows %+v, %v", src, err)
	}
	want := Source{URL: "git@github.com:me/app.git", Branch: "main", DeployKey: []byte("PRIVATE KEY"), WebhookSecret: "s3cret"}
	must(t, s.SetSource(ctx, p.ID, want))
	got, err := s.Source(ctx, p.ID)
	if err != nil || got.URL != want.URL || got.Branch != "main" || string(got.DeployKey) != "PRIVATE KEY" || got.WebhookSecret != "s3cret" {
		t.Fatalf("source = %+v, %v", got, err)
	}
	var raw []byte
	must(t, s.db.QueryRowContext(ctx, "SELECT deploy_key FROM project").Scan(&raw))
	if bytes.Contains(raw, []byte("PRIVATE KEY")) {
		t.Error("deploy key stored in plaintext")
	}
	must(t, s.ClearSource(ctx, p.ID))
	if src, _ := s.Source(ctx, p.ID); src.URL != "" || src.DeployKey != nil {
		t.Errorf("source left after clear: %+v", src)
	}
}
