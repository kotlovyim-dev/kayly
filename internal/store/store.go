// Package store keeps Kayly's desired state in SQLite: projects, releases, env values and
// domains. Secret values are encrypted before they reach the database.
package store

import (
	"context"
	"database/sql"
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
}

const releaseCols = "id, project_id, seq, status, config, image, env, error, created_at"

func (s *Store) scanRelease(row interface{ Scan(...any) error }) (Release, error) {
	var r Release
	var env []byte
	var created int64
	if err := row.Scan(&r.ID, &r.ProjectID, &r.Seq, &r.Status, &r.Config, &r.Image, &env, &r.Error, &created); err != nil {
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
func (s *Store) CreateRelease(ctx context.Context, projectID int64, config []byte) (Release, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO release (project_id, seq, status, config, created_at)
		VALUES (?, (SELECT coalesce(max(seq), 0) + 1 FROM release WHERE project_id = ?), ?, ?, ?)
		RETURNING `+releaseCols, projectID, projectID, StatusBuilding, config, time.Now().Unix())
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
