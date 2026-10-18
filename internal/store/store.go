// Package store keeps Kayly's desired state in SQLite: projects, releases, env values and
// domains. Secret values are encrypted before they reach the database.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/secret"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a looked-up row does not exist.
var ErrNotFound = errors.New("not found")

// Release statuses. A release moves forward only:
// building → releasing → starting → live → superseded, or to failed from any non-final state.
const (
	StatusBuilding   = "building"
	StatusReleasing  = "releasing"
	StatusStarting   = "starting"
	StatusLive       = "live"
	StatusFailed     = "failed"
	StatusSuperseded = "superseded"
)

// Store is the SQLite-backed state of one Kayly daemon.
type Store struct {
	db  *sql.DB
	box *secret.Box
}

// Open opens (creating if needed) the database at path and applies pending migrations.
func Open(ctx context.Context, path string, box *secret.Box) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// ponytail: one connection serializes every query; the daemon's load is a handful of
	// writes per deploy. Allow more readers if the UI ever gets slow.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, box: box}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrations are applied in order; PRAGMA user_version records how many ran.
// Never edit a migration that has been released: append a new one.
var migrations = []string{`
CREATE TABLE project (
	id             INTEGER PRIMARY KEY,
	name           TEXT NOT NULL UNIQUE,
	active_release INTEGER REFERENCES release(id),
	created_at     INTEGER NOT NULL
);
CREATE TABLE release (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
	seq        INTEGER NOT NULL,
	status     TEXT NOT NULL CHECK (status IN ('building','releasing','starting','live','failed','superseded')),
	config     BLOB NOT NULL,
	image      TEXT NOT NULL DEFAULT '',
	env        BLOB,
	error      TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	UNIQUE (project_id, seq)
);
CREATE TABLE env_var (
	project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	value      BLOB NOT NULL,
	PRIMARY KEY (project_id, key)
);
CREATE TABLE domain (
	hostname   TEXT PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
	service    TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
`, `
CREATE TABLE token (
	id           INTEGER PRIMARY KEY,
	name         TEXT NOT NULL,
	hash         BLOB NOT NULL UNIQUE,
	created_at   INTEGER NOT NULL,
	last_used_at INTEGER
);
`, `
CREATE TABLE addon (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
	kind       TEXT NOT NULL,
	image      TEXT NOT NULL,
	password   BLOB NOT NULL,
	created_at INTEGER NOT NULL,
	UNIQUE (project_id, kind)
);
`, `
CREATE TABLE setting (
	key   TEXT PRIMARY KEY,
	value BLOB NOT NULL
);
`, `
ALTER TABLE release ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE release ADD COLUMN commit_msg TEXT NOT NULL DEFAULT '';
`, `
ALTER TABLE token ADD COLUMN scope TEXT NOT NULL DEFAULT 'admin' CHECK (scope IN ('read','deploy','admin'));
ALTER TABLE token ADD COLUMN project_id INTEGER REFERENCES project(id) ON DELETE CASCADE;
ALTER TABLE token ADD COLUMN expires_at INTEGER;
CREATE TABLE audit (
	id         INTEGER PRIMARY KEY,
	token      TEXT NOT NULL,
	action     TEXT NOT NULL,
	project    TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
`, `
ALTER TABLE project ADD COLUMN git_url TEXT NOT NULL DEFAULT '';
ALTER TABLE project ADD COLUMN git_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE project ADD COLUMN deploy_key BLOB;
ALTER TABLE project ADD COLUMN webhook_secret BLOB;
`}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("store: database schema v%d is newer than this build (v%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		err := s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1))
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migration %d: %w", i+1, err)
		}
	}
	return nil
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Project is a deployable app.
type Project struct {
	ID   int64
	Name string
	// ActiveRelease is the release serving traffic, 0 before the first successful deploy.
	ActiveRelease int64
	CreatedAt     time.Time
}

const projectCols = "id, name, coalesce(active_release, 0), created_at"

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var created int64
	err := row.Scan(&p.ID, &p.Name, &p.ActiveRelease, &created)
	p.CreatedAt = time.Unix(created, 0)
	return p, err
}

// CreateProject adds a project. Names are validated by the caller.
func (s *Store) CreateProject(ctx context.Context, name string) (Project, error) {
	row := s.db.QueryRowContext(ctx,
		"INSERT INTO project (name, created_at) VALUES (?, ?) RETURNING "+projectCols, name, time.Now().Unix())
	p, err := scanProject(row)
	if err != nil {
		return Project{}, fmt.Errorf("store: create project %s: %w", name, err)
	}
	return p, nil
}

// Project returns the project called name.
func (s *Store) Project(ctx context.Context, name string) (Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, "SELECT "+projectCols+" FROM project WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("project %s: %w", name, ErrNotFound)
	}
	return p, err
}

// Source is the git repository a project follows. The zero value means none.
type Source struct {
	URL, Branch string
	// DeployKey is the private SSH key; WebhookSecret signs the GitHub push webhook.
	DeployKey     []byte
	WebhookSecret string
}

// SetSource makes the project follow a repository.
func (s *Store) SetSource(ctx context.Context, projectID int64, src Source) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE project SET git_url = ?, git_branch = ?, deploy_key = ?, webhook_secret = ? WHERE id = ?",
		src.URL, src.Branch, s.box.Seal(src.DeployKey), s.box.Seal([]byte(src.WebhookSecret)), projectID)
	if err != nil {
		return fmt.Errorf("store: set source: %w", err)
	}
	return nil
}

// Source returns the repository a project follows, or the zero Source.
func (s *Store) Source(ctx context.Context, projectID int64) (Source, error) {
	var src Source
	var key, secret []byte
	err := s.db.QueryRowContext(ctx, "SELECT git_url, git_branch, deploy_key, webhook_secret FROM project WHERE id = ?", projectID).
		Scan(&src.URL, &src.Branch, &key, &secret)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, fmt.Errorf("project %d: %w", projectID, ErrNotFound)
	}
	if err != nil || src.URL == "" {
		return Source{}, err
	}
	if src.DeployKey, err = s.box.Open(key); err != nil {
		return Source{}, err
	}
	sec, err := s.box.Open(secret)
	src.WebhookSecret = string(sec)
	return src, err
}

// ClearSource stops a project following a repository and forgets its keys.
func (s *Store) ClearSource(ctx context.Context, projectID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE project SET git_url = '', git_branch = '', deploy_key = NULL, webhook_secret = NULL WHERE id = ?", projectID)
	return err
}

// DeleteProject removes a project with its releases, env, domains, add-ons and tokens.
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM project WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %d: %w", id, ErrNotFound)
	}
	return nil
}

// Projects returns every project ordered by name.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+projectCols+" FROM project ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Release is one immutable build of a project: image, config and resolved env.
type Release struct {
	ID        int64
	ProjectID int64
	Seq       int
	Status    string
	// Config is the kayly.toml the release was built from.
	Config []byte
	Image  string
	// Env is the resolved environment per target (service name or "release").
	Env       map[string]map[string]string
	Error     string
	CreatedAt time.Time
	// Commit is the source the release was built from; empty when the source had none.
	Commit Commit
}

// Commit identifies the source of a release. SHA ends in "-dirty" when the work tree it was
// built from had uncommitted changes.
type Commit struct {
	SHA     string
	Message string
}

const releaseCols = "id, project_id, seq, status, config, image, env, error, created_at, commit_sha, commit_msg"

func (s *Store) scanRelease(row interface{ Scan(...any) error }) (Release, error) {
	var r Release
	var env []byte
	var created int64
	if err := row.Scan(&r.ID, &r.ProjectID, &r.Seq, &r.Status, &r.Config, &r.Image, &env, &r.Error, &created, &r.Commit.SHA, &r.Commit.Message); err != nil {
		return Release{}, err
	}
	r.CreatedAt = time.Unix(created, 0)
	if env != nil {
		pt, err := s.box.Open(env)
		if err != nil {
			return Release{}, fmt.Errorf("release %d env: %w", r.ID, err)
		}
		if err := json.Unmarshal(pt, &r.Env); err != nil {
			return Release{}, fmt.Errorf("release %d env: %w", r.ID, err)
		}
	}
	return r, nil
}

// CreateRelease records a new release in status building with the next sequence number.
func (s *Store) CreateRelease(ctx context.Context, projectID int64, config []byte, commit Commit) (Release, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO release (project_id, seq, status, config, created_at, commit_sha, commit_msg)
		VALUES (?, (SELECT coalesce(max(seq), 0) + 1 FROM release WHERE project_id = ?), ?, ?, ?, ?, ?)
		RETURNING `+releaseCols, projectID, projectID, StatusBuilding, config, time.Now().Unix(), commit.SHA, commit.Message)
	r, err := s.scanRelease(row)
	if err != nil {
		return Release{}, fmt.Errorf("store: create release: %w", err)
	}
	return r, nil
}

// Release returns a project's release by sequence number.
func (s *Store) Release(ctx context.Context, projectID int64, seq int) (Release, error) {
	r, err := s.scanRelease(s.db.QueryRowContext(ctx,
		"SELECT "+releaseCols+" FROM release WHERE project_id = ? AND seq = ?", projectID, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, fmt.Errorf("release r%d: %w", seq, ErrNotFound)
	}
	return r, err
}

// ReleaseByID returns a release by its id.
func (s *Store) ReleaseByID(ctx context.Context, id int64) (Release, error) {
	r, err := s.scanRelease(s.db.QueryRowContext(ctx, "SELECT "+releaseCols+" FROM release WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, fmt.Errorf("release %d: %w", id, ErrNotFound)
	}
	return r, err
}

// Releases returns a project's latest releases, newest first.
func (s *Store) Releases(ctx context.Context, projectID int64, limit int) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+releaseCols+" FROM release WHERE project_id = ? ORDER BY seq DESC LIMIT ?", projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		r, err := s.scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetBuilt records the image and resolved env of a release and moves it to releasing.
func (s *Store) SetBuilt(ctx context.Context, id int64, image string, env map[string]map[string]string) error {
	pt, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return s.transition(ctx, id, StatusBuilding, "UPDATE release SET image = ?, env = ?, status = ? WHERE id = ? AND status = ?",
		image, s.box.Seal(pt), StatusReleasing, id, StatusBuilding)
}

// SetStarting moves a release from releasing to starting.
func (s *Store) SetStarting(ctx context.Context, id int64) error {
	return s.transition(ctx, id, StatusReleasing, "UPDATE release SET status = ? WHERE id = ? AND status = ?", StatusStarting, id, StatusReleasing)
}

// Fail marks a release that has not gone live as failed.
func (s *Store) Fail(ctx context.Context, id int64, reason string) error {
	return s.transition(ctx, id, "in progress", "UPDATE release SET status = ?, error = ? WHERE id = ? AND status IN (?, ?, ?)",
		StatusFailed, reason, id, StatusBuilding, StatusReleasing, StatusStarting)
}

// FailInterrupted marks every release stuck mid-pipeline as failed and returns them.
// Call it only when no pipeline is running, e.g. on daemon start.
func (s *Store) FailInterrupted(ctx context.Context) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE release SET status = ?, error = 'interrupted: the daemon stopped during the deploy'
		WHERE status IN (?, ?, ?) RETURNING `+releaseCols,
		StatusFailed, StatusBuilding, StatusReleasing, StatusStarting)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		r, err := s.scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Activate makes a release the one serving traffic: it becomes live, the previous active
// release becomes superseded, all in one transaction. The release must be starting (a
// deploy) or already built and not failed (a rollback to a superseded release).
func (s *Store) Activate(ctx context.Context, projectID, releaseID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE release SET status = ?, error = ''
			WHERE id = ? AND project_id = ? AND status IN (?, ?, ?)`,
			StatusLive, releaseID, projectID, StatusStarting, StatusSuperseded, StatusLive)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("store: release %d cannot be activated", releaseID)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE release SET status = ?
			WHERE project_id = ? AND status = ? AND id != ?`,
			StatusSuperseded, projectID, StatusLive, releaseID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE project SET active_release = ? WHERE id = ?", releaseID, projectID)
		return err
	})
}

// transition runs an UPDATE guarded by the release's current status and fails if the
// release was not in the status the caller expected.
func (s *Store) transition(ctx context.Context, id int64, from, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: release %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: release %d is not %s", id, from)
	}
	return nil
}

// Env returns a project's stored env values, decrypted.
func (s *Store) Env(ctx context.Context, projectID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM env_var WHERE project_id = ?", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key string
		var sealed []byte
		if err := rows.Scan(&key, &sealed); err != nil {
			return nil, err
		}
		v, err := s.box.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", key, err)
		}
		out[key] = string(v)
	}
	return out, rows.Err()
}

// SetEnv stores (or replaces) one env value.
func (s *Store) SetEnv(ctx context.Context, projectID int64, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO env_var (project_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT (project_id, key) DO UPDATE SET value = excluded.value`,
		projectID, key, s.box.Seal([]byte(value)))
	return err
}

// DeleteEnv removes one env value.
func (s *Store) DeleteEnv(ctx context.Context, projectID int64, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM env_var WHERE project_id = ? AND key = ?", projectID, key)
	return err
}

// Domain routes a hostname to a service of a project.
type Domain struct {
	Hostname  string
	ProjectID int64
	Service   string
}

// Domains returns a project's domains, oldest first.
func (s *Store) Domains(ctx context.Context, projectID int64) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT hostname, project_id, service FROM domain WHERE project_id = ? ORDER BY created_at, hostname", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.Hostname, &d.ProjectID, &d.Service); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddDomain routes hostname to a service. A hostname belongs to at most one project.
func (s *Store) AddDomain(ctx context.Context, d Domain) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO domain (hostname, project_id, service, created_at) VALUES (?, ?, ?, ?)",
		d.Hostname, d.ProjectID, d.Service, time.Now().Unix())
	return err
}

// RemoveDomain deletes a domain.
func (s *Store) RemoveDomain(ctx context.Context, hostname string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM domain WHERE hostname = ?", hostname)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("domain %s: %w", hostname, ErrNotFound)
	}
	return nil
}

// Token scopes, each including the ones before it.
const (
	ScopeRead   = "read"
	ScopeDeploy = "deploy"
	ScopeAdmin  = "admin"
)

var scopeRank = map[string]int{ScopeRead: 1, ScopeDeploy: 2, ScopeAdmin: 3}

// ValidScope reports whether scope is one of the token scopes.
func ValidScope(scope string) bool { return scopeRank[scope] != 0 }

// Token is an API token. Only its SHA-256 is stored.
type Token struct {
	ID    int64
	Name  string
	Scope string
	// Project is the one project the token is limited to; empty means every project.
	Project   string
	ExpiresAt time.Time // zero: never
	CreatedAt time.Time
	// LastUsedAt is zero until the token is first used.
	LastUsedAt time.Time
}

// Allows reports whether the token's scope covers scope.
func (t Token) Allows(scope string) bool { return scopeRank[t.Scope] >= scopeRank[scope] }

// NewToken describes a token to create. ProjectID 0 and a zero ExpiresAt mean no limit.
type NewToken struct {
	Name      string
	Scope     string
	ProjectID int64
	ExpiresAt time.Time
}

// CreateToken generates a token, stores its hash and returns the plaintext once.
func (s *Store) CreateToken(ctx context.Context, n NewToken) (plaintext string, err error) {
	if !ValidScope(n.Scope) {
		return "", fmt.Errorf("store: create token: unknown scope %q", n.Scope)
	}
	buf := make([]byte, 32)
	rand.Read(buf)
	plaintext = "kayly_" + base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	var project, expires any
	if n.ProjectID != 0 {
		project = n.ProjectID
	}
	if !n.ExpiresAt.IsZero() {
		expires = n.ExpiresAt.Unix()
	}
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO token (name, hash, scope, project_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		n.Name, sum[:], n.Scope, project, expires, time.Now().Unix())
	if err != nil {
		return "", fmt.Errorf("store: create token: %w", err)
	}
	return plaintext, nil
}

const tokenSelect = `SELECT t.id, t.name, t.scope, coalesce(p.name, ''), coalesce(t.expires_at, 0), t.created_at, coalesce(t.last_used_at, 0)
	FROM token t LEFT JOIN project p ON p.id = t.project_id`

func scanToken(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	var expires, created, used int64
	if err := row.Scan(&t.ID, &t.Name, &t.Scope, &t.Project, &expires, &created, &used); err != nil {
		return Token{}, err
	}
	t.CreatedAt = time.Unix(created, 0)
	if expires != 0 {
		t.ExpiresAt = time.Unix(expires, 0)
	}
	if used != 0 {
		t.LastUsedAt = time.Unix(used, 0)
	}
	return t, nil
}

// LookupToken returns the token behind plaintext and records its use. An unknown or expired
// token is ErrNotFound.
func (s *Store) LookupToken(ctx context.Context, plaintext string) (Token, error) {
	sum := sha256.Sum256([]byte(plaintext))
	t, err := scanToken(s.db.QueryRowContext(ctx, tokenSelect+" WHERE t.hash = ?", sum[:]))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(time.Now())) {
		return Token{}, fmt.Errorf("token: %w", ErrNotFound)
	}
	if err != nil {
		return Token{}, err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE token SET last_used_at = ? WHERE id = ?", time.Now().Unix(), t.ID)
	return t, err
}

// Tokens lists API tokens, oldest first.
func (s *Store) Tokens(ctx context.Context) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx, tokenSelect+" ORDER BY t.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteToken revokes a token.
func (s *Store) DeleteToken(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM token WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("token %d: %w", id, ErrNotFound)
	}
	return nil
}

// AuditEntry is one change made through the API. Token is the token's name, kept after the
// token is revoked; it is "local" for the root-only admin socket.
type AuditEntry struct {
	Token, Action, Project string
	At                     time.Time
}

// Audit records a change.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO audit (token, action, project, created_at) VALUES (?, ?, ?, ?)",
		e.Token, e.Action, e.Project, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("store: audit: %w", err)
	}
	return nil
}

// AuditLog returns the newest limit entries, newest first.
func (s *Store) AuditLog(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT token, action, project, created_at FROM audit ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.Token, &e.Action, &e.Project, &at); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Addon is a stateful resource attached to a project, such as a Postgres server.
type Addon struct {
	ProjectID int64
	Kind      string
	// Image is the pinned image the add-on was created with. Upgrades are explicit.
	Image     string
	Password  string
	CreatedAt time.Time
}

// AddAddon attaches an add-on to a project. A project has at most one of each kind.
func (s *Store) AddAddon(ctx context.Context, a Addon) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO addon (project_id, kind, image, password, created_at) VALUES (?, ?, ?, ?, ?)",
		a.ProjectID, a.Kind, a.Image, s.box.Seal([]byte(a.Password)), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("store: add %s: %w", a.Kind, err)
	}
	return nil
}

// Addons returns a project's add-ons ordered by kind.
func (s *Store) Addons(ctx context.Context, projectID int64) ([]Addon, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT project_id, kind, image, password, created_at FROM addon WHERE project_id = ? ORDER BY kind", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Addon
	for rows.Next() {
		var a Addon
		var sealed []byte
		var created int64
		if err := rows.Scan(&a.ProjectID, &a.Kind, &a.Image, &sealed, &created); err != nil {
			return nil, err
		}
		pw, err := s.box.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("add-on %s: %w", a.Kind, err)
		}
		a.Password, a.CreatedAt = string(pw), time.Unix(created, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// RemoveAddon detaches an add-on.
func (s *Store) RemoveAddon(ctx context.Context, projectID int64, kind string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM addon WHERE project_id = ? AND kind = ?", projectID, kind)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("add-on %s: %w", kind, ErrNotFound)
	}
	return nil
}

// Setting returns a server-wide value, or "" if it was never set. Values are encrypted.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM setting WHERE key = ?", key).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: setting %s: %w", key, err)
	}
	v, err := s.box.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("store: setting %s: %w", key, err)
	}
	return string(v), nil
}

// SetSetting stores a server-wide value; an empty value removes it.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	var err error
	if value == "" {
		_, err = s.db.ExecContext(ctx, "DELETE FROM setting WHERE key = ?", key)
	} else {
		_, err = s.db.ExecContext(ctx,
			"INSERT INTO setting (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
			key, s.box.Seal([]byte(value)))
	}
	if err != nil {
		return fmt.Errorf("store: set %s: %w", key, err)
	}
	return nil
}

// Snapshot writes a consistent copy of the database to path, which must not exist or be
// empty. It is safe while the daemon is writing.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("store: snapshot: %w", err)
	}
	return nil
}
