// Package api serves Kayly's HTTP API, used by the CLI, the UI and agents.
//
// Errors have one shape: {"error": {"code", "message", "hint"}}. Long operations (deploy,
// rollback) answer 200 with an NDJSON stream of {"log": "..."} events ending in one
// {"result": {...}} event, because their outcome is only known at the end.
package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/gitsrc"
	"github.com/kotlovyim-dev/kayly/internal/notify"
	"github.com/kotlovyim-dev/kayly/internal/offsite"
	"github.com/kotlovyim-dev/kayly/internal/ratelimit"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// Server holds the API's dependencies.
type Server struct {
	store    *store.Store
	deployer *deploy.Deployer
	// workDir is where uploaded sources are unpacked for a build.
	workDir string
	log     *slog.Logger
	// UI holds the built web UI (index.html at its root), served on the public listener.
	UI fs.FS
	// Notifier delivers alerts and holds their targets. Nil disables the notification routes' effect.
	Notifier *notify.Notifier
	// PublicURL is where the internet reaches the daemon, e.g. https://kayly.example.com.
	PublicURL string
	// Git fetches the sources of projects that follow a repository. Nil disables that.
	Git *gitsrc.Fetcher
	// Offsite uploads backups to the owner's bucket.
	Offsite *offsite.Manager
	// Version is Kayly's own version, shown by the server status.
	Version string
	// guesses slows down an address that keeps presenting tokens that do not exist.
	guesses *ratelimit.Limiter
}

// A client address may present this many unknown tokens in a row, then one a minute.
const (
	guessBurst = 10
	guessEvery = time.Minute
)

// New returns a Server. workDir must exist.
func New(st *store.Store, d *deploy.Deployer, workDir string, log *slog.Logger) *Server {
	return &Server{store: st, deployer: d, workDir: workDir, log: log, guesses: ratelimit.New(guessBurst, guessEvery)}
}

// Handler returns the API. A trusted handler (the root-only admin socket) needs no token
// and may manage tokens; an untrusted one (behind the proxy) requires a bearer token.
func (s *Server) Handler(trusted bool) http.Handler {
	mux := http.NewServeMux()
	// handle registers a route with the scope a token needs to call it. Routes that change
	// anything are audited. Everything under {project} is limited to a bound token's project.
	handle := func(pattern, scope string, h http.HandlerFunc) {
		mux.Handle(pattern, s.authorize(pattern, scope, h))
	}
	const read, deployScope, admin = store.ScopeRead, store.ScopeDeploy, store.ScopeAdmin
	handle("GET /api/v1/projects", read, s.listProjects)
	handle("POST /api/v1/projects", admin, s.createProject)
	handle("GET /api/v1/projects/{project}", read, s.getProject)
	handle("DELETE /api/v1/projects/{project}", admin, s.deleteProject)
	handle("GET /api/v1/projects/{project}/services", read, s.listServices)
	handle("POST /api/v1/projects/{project}/services/{service}/restart", deployScope, s.restartService)
	handle("GET /api/v1/server", read, s.serverStatus)
	handle("GET /api/v1/projects/{project}/releases", read, s.listReleases)
	handle("GET /api/v1/projects/{project}/releases/{seq}/log", read, s.releaseLog)
	handle("POST /api/v1/projects/{project}/redeploy", deployScope, s.redeploy)
	handle("GET /api/v1/projects/{project}/addons", read, s.listAddons)
	handle("POST /api/v1/projects/{project}/addons", admin, s.addAddon)
	handle("DELETE /api/v1/projects/{project}/addons/{kind}", admin, s.removeAddon)
	handle("GET /api/v1/projects/{project}/backups", read, s.listBackups)
	handle("POST /api/v1/projects/{project}/backups", deployScope, s.createBackup)
	handle("POST /api/v1/projects/{project}/backups/restore", admin, s.restoreBackup)
	handle("GET /api/v1/projects/{project}/backups/{name}", admin, s.downloadBackup)
	handle("POST /api/v1/projects/{project}/deploys", deployScope, s.deploy)
	handle("POST /api/v1/projects/{project}/rollback", deployScope, s.rollback)
	handle("GET /api/v1/projects/{project}/logs", read, s.logs)
	handle("GET /api/v1/projects/{project}/env", read, s.listEnv)
	handle("PUT /api/v1/projects/{project}/env/{key}", deployScope, s.setEnv)
	handle("DELETE /api/v1/projects/{project}/env/{key}", deployScope, s.deleteEnv)
	handle("GET /api/v1/projects/{project}/domains", read, s.listDomains)
	handle("POST /api/v1/projects/{project}/domains", deployScope, s.addDomain)
	handle("DELETE /api/v1/projects/{project}/domains/{hostname}", deployScope, s.removeDomain)
	handle("GET /api/v1/notifications", admin, s.getNotifications)
	handle("PUT /api/v1/notifications", admin, s.setNotifications)
	handle("POST /api/v1/notifications/test", admin, s.testNotifications)
	handle("POST /api/v1/backups/kayly", admin, s.backupKayly)
	handle("GET /api/v1/offsite", admin, s.getOffsite)
	handle("PUT /api/v1/offsite", admin, s.setOffsite)
	handle("POST /api/v1/offsite/sync", admin, s.syncOffsite)
	handle("POST /api/v1/offsite/drill", admin, s.drillOffsite)
	handle("GET /api/v1/projects/{project}/source", read, s.getSource)
	handle("PUT /api/v1/projects/{project}/source", admin, s.setSource)
	handle("DELETE /api/v1/projects/{project}/source", admin, s.deleteSource)
	handle("POST /api/v1/projects/{project}/deploys/git", deployScope, s.deployFromGit)
	handle("GET /api/v1/audit", admin, s.auditLog)
	if trusted {
		mux.HandleFunc("GET /api/v1/tokens", s.listTokens)
		mux.HandleFunc("POST /api/v1/tokens", s.createToken)
		mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.deleteToken)
		return withToken(localToken, mux)
	}
	root := http.NewServeMux()
	root.HandleFunc("POST /api/v1/session", s.login)
	root.HandleFunc("DELETE /api/v1/session", s.logout)
	root.HandleFunc("POST /api/v1/hooks/github/{project}", s.githubHook)
	root.Handle("/mcp", s.requireToken(s.mcpHandler()))
	root.Handle("/api/", s.requireToken(mux))
	if s.UI != nil {
		root.Handle("/", uiHandler(s.UI))
	}
	return root
}

// localToken stands for whoever can open the root-only admin socket.
var localToken = store.Token{Name: "local", Scope: store.ScopeAdmin}

type tokenKey struct{}

func withToken(t store.Token, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, t)))
	})
}

// boundOK lists the routes outside a project that a project-bound token may still call; they
// show only that project (see listProjects) or nothing project-specific.
var boundOK = map[string]bool{"GET /api/v1/projects": true, "GET /api/v1/server": true}

// audit records a change made by a handler itself (token routes bypass authorize).
func (s *Server) audit(r *http.Request, action, project string) {
	tok, _ := r.Context().Value(tokenKey{}).(store.Token)
	if err := s.store.Audit(r.Context(), store.AuditEntry{Token: tok.Name, Action: action, Project: project}); err != nil {
		s.log.Warn("audit failed", "action", action, "err", err)
	}
}

// denial says why tok may not do something that needs scope on project ("" for a call that
// names no project), or "" if it may. A project-bound token reaches only its own project, and
// of the calls that name none only those with projectFree.
func denial(tok store.Token, scope, project string, projectFree bool) (msg, hint string) {
	if !tok.Allows(scope) {
		return fmt.Sprintf("this token has the %s scope; this needs %s", tok.Scope, scope),
			"create a token with a wider scope: kayly token create <name> -scope " + scope
	}
	if tok.Project != "" && (project != "" && project != tok.Project || project == "" && !projectFree) {
		return "this token is limited to project " + tok.Project, ""
	}
	return "", ""
}

// authorize refuses a request whose token has too little scope or belongs to another
// project, and audits the ones that change something.
func (s *Server) authorize(pattern, scope string, next http.HandlerFunc) http.Handler {
	write := !strings.HasPrefix(pattern, "GET ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, _ := r.Context().Value(tokenKey{}).(store.Token)
		name := r.PathValue("project")
		if msg, hint := denial(tok, scope, name, boundOK[pattern]); msg != "" {
			writeError(w, http.StatusForbidden, "forbidden", msg, hint)
			return
		}
		if write {
			if err := s.store.Audit(r.Context(), store.AuditEntry{Token: tok.Name, Action: pattern, Project: name}); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		next(w, r)
	})
}

// sessionCookie carries an API token for the web UI.
const sessionCookie = "kayly_session"

// csrfHeader must accompany every cookie-authenticated request that changes something. A
// cross-site form cannot set it, and the cookie is SameSite=Strict besides.
const csrfHeader = "X-Kayly-Request"

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			if c, err := r.Cookie(sessionCookie); err == nil {
				tok, ok = c.Value, true
				if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) == "" {
					writeError(w, http.StatusForbidden, "csrf", "missing "+csrfHeader+" header", "")
					return
				}
			}
		}
		var t store.Token
		if ok {
			if s.tooManyGuesses(w, r) {
				return
			}
			var err error
			t, err = s.store.LookupToken(r.Context(), tok)
			if errors.Is(err, store.ErrNotFound) {
				ok = false
				s.guesses.Fail(clientAddr(r))
			} else if err != nil {
				s.fail(w, r, err)
				return
			}
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing, invalid or expired token",
				"create one on the server with `kayly token create <name>`, then `kayly login`")
			return
		}
		withToken(t, next).ServeHTTP(w, r)
	})
}

// login trades an API token for a session cookie.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &body) || s.tooManyGuesses(w, r) {
		return
	}
	_, err := s.store.LookupToken(r.Context(), strings.TrimSpace(body.Token))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	if err != nil {
		s.guesses.Fail(clientAddr(r))
		writeError(w, http.StatusUnauthorized, "unauthorized", "this token is not valid",
			"create one on the server with `kayly token create <name>`")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: strings.TrimSpace(body.Token), Path: "/",
		MaxAge: 30 * 24 * 3600, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// tooManyGuesses answers 429 and reports true when the request's address has presented too many
// unknown tokens lately. Tokens are long random strings; this keeps guessing from being free and
// keeps the log quiet.
func (s *Server) tooManyGuesses(w http.ResponseWriter, r *http.Request) bool {
	wait := s.guesses.Wait(clientAddr(r))
	if wait == 0 {
		return false
	}
	secs := int(wait.Round(time.Second) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many invalid tokens from this address",
		fmt.Sprintf("wait %d seconds, then try again with a valid token", max(secs, 1)))
	return true
}

// clientAddr is the address a request came from. The public listener is reached only through
// the proxy, which replaces X-Forwarded-For with the client's address; the last entry is the
// one the proxy wrote.
func clientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(xff[strings.LastIndexByte(xff, ',')+1:])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// uiHandler serves the single-page UI: files as they are, any other path as index.html.
// Hashed assets are cached for good; everything else is revalidated.
func uiHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if fi, err := fs.Stat(ui, name); name == "" || err != nil || fi.IsDir() {
			http.ServeFileFS(w, r, ui, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}

// ProjectView is a project as the API shows it.
type ProjectView struct {
	Name      string `json:"name"`
	LiveSeq   int    `json:"live_release,omitempty"`
	LiveSince string `json:"live_since,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	out, err := s.projectViews(r.Context(), r.Context().Value(tokenKey{}).(store.Token))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// projectViews lists the projects tok may see.
func (s *Server) projectViews(ctx context.Context, tok store.Token) ([]ProjectView, error) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return nil, err
	}
	out := []ProjectView{}
	for _, p := range projects {
		if tok.Project != "" && p.Name != tok.Project {
			continue
		}
		v := ProjectView{Name: p.Name, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}
		if p.ActiveRelease != 0 {
			rel, err := s.store.ReleaseByID(ctx, p.ActiveRelease)
			if err != nil {
				return nil, err
			}
			v.LiveSeq, v.LiveSince = rel.Seq, rel.CreatedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !config.ValidName(body.Name) {
		writeError(w, http.StatusBadRequest, "invalid_name", fmt.Sprintf("%q is not a valid project name", body.Name),
			"use lowercase letters, digits and dashes, starting with a letter, up to 31 characters")
		return
	}
	p, err := s.store.CreateProject(r.Context(), body.Name)
	if err != nil {
		if _, gerr := s.store.Project(r.Context(), body.Name); gerr == nil {
			writeError(w, http.StatusConflict, "exists", "project "+body.Name+" already exists", "")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ProjectView{Name: p.Name, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)})
}

// ProjectDetail is everything the project page shows at once.
type ProjectDetail struct {
	ProjectView
	Services []deploy.ServiceView `json:"services"`
	Addons   []deploy.AddonView   `json:"addons"`
	Releases []ReleaseView        `json:"releases"`
	Domains  []DomainView         `json:"domains"`
	EnvKeys  []string             `json:"env_keys"`
	// MissingEnv are keys the live release declares and needs but has no value for: the next
	// deploy would fail on them.
	MissingEnv []string `json:"missing_env"`
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	d, err := s.projectDetail(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Addons = redactAddons(r.Context().Value(tokenKey{}).(store.Token), d.Addons)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) projectDetail(ctx context.Context, p store.Project) (ProjectDetail, error) {
	d := ProjectDetail{ProjectView: ProjectView{Name: p.Name, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}}
	var err error
	if d.Services, err = s.deployer.Services(ctx, p.Name, false); err != nil {
		return d, err
	}
	if d.Addons, err = s.deployer.Addons(ctx, p.Name); err != nil {
		return d, err
	}
	rels, err := s.store.Releases(ctx, p.ID, 20)
	if err != nil {
		return d, err
	}
	d.Releases = []ReleaseView{}
	var live *config.Config
	for _, rel := range rels {
		v := releaseView(rel)
		v.CanRollBack = s.deployer.CanRollBack(ctx, rel)
		d.Releases = append(d.Releases, v)
		if rel.ID == p.ActiveRelease {
			d.LiveSeq, d.LiveSince = rel.Seq, rel.CreatedAt.UTC().Format(time.RFC3339)
			live, _ = config.Parse(rel.Config)
		}
	}
	ds, err := s.store.Domains(ctx, p.ID)
	if err != nil {
		return d, err
	}
	d.Domains = []DomainView{}
	for _, dm := range ds {
		d.Domains = append(d.Domains, DomainView{Hostname: dm.Hostname, Service: dm.Service})
	}
	env, err := s.store.Env(ctx, p.ID)
	if err != nil {
		return d, err
	}
	d.EnvKeys = slices.Sorted(maps.Keys(env))
	d.MissingEnv = []string{}
	if live != nil {
		for _, key := range slices.Sorted(maps.Keys(live.Env)) {
			spec := live.Env[key]
			if _, set := env[key]; !set && spec.IsRequired() && spec.Generate == "" && spec.Template == "" {
				d.MissingEnv = append(d.MissingEnv, key)
			}
		}
	}
	return d, nil
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if r.URL.Query().Get("confirm") != name {
		writeError(w, http.StatusBadRequest, "confirm_required", "deleting a project removes its containers, add-ons and volumes",
			"confirm with the project name: DELETE ...?confirm="+name)
		return
	}
	if err := s.deployer.DeleteProject(context.WithoutCancel(r.Context()), name); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restartService(w http.ResponseWriter, r *http.Request) {
	if err := s.deployer.RestartService(context.WithoutCancel(r.Context()), r.PathValue("project"), r.PathValue("service")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) redeploy(w http.ResponseWriter, r *http.Request) {
	stream := newStream(w)
	rel, err := s.deployer.Redeploy(context.WithoutCancel(r.Context()), r.PathValue("project"), stream)
	stream.end(Result{OK: err == nil, Release: rel.Seq, Error: errString(err)})
}

func (s *Server) listAddons(w http.ResponseWriter, r *http.Request) {
	as, err := s.deployer.Addons(r.Context(), r.PathValue("project"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, redactAddons(r.Context().Value(tokenKey{}).(store.Token), as))
}

// redactAddons hides the connection URLs, which carry the superuser password, from tokens
// below admin.
func redactAddons(tok store.Token, as []deploy.AddonView) []deploy.AddonView {
	if tok.Allows(store.ScopeAdmin) {
		return as
	}
	out := slices.Clone(as)
	for i := range out {
		out[i].URL = ""
	}
	return out
}

func (s *Server) addAddon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	// Pulling an image and starting a database outlive an impatient client.
	if _, err := s.deployer.AddAddon(context.WithoutCancel(r.Context()), r.PathValue("project"), body.Kind); err != nil {
		s.fail(w, r, err)
		return
	}
	as, err := s.deployer.Addons(r.Context(), r.PathValue("project"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, as)
}

func (s *Server) removeAddon(w http.ResponseWriter, r *http.Request) {
	if err := s.deployer.RemoveAddon(r.Context(), r.PathValue("project"), r.PathValue("kind")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.project(w, r); !ok {
		return
	}
	var bs []deploy.Backup
	var err error
	if r.URL.Query().Get("remote") == "1" {
		bs, err = s.deployer.RemoteBackups(r.Context(), r.PathValue("project"))
	} else {
		bs, err = s.deployer.Backups(r.PathValue("project"))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	bs, err := s.deployer.BackupNow(context.WithoutCancel(r.Context()), r.PathValue("project"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if bs == nil {
		bs = []deploy.Backup{}
	}
	writeJSON(w, http.StatusCreated, bs)
}

// restoreBackup replaces live data with a backup. Like every destructive call it needs the
// project's name as confirmation, so a script cannot do it by accident.
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.project(w, r); !ok {
		return
	}
	project := r.PathValue("project")
	var body struct {
		Name        string `json:"name"`
		FromProject string `json:"from_project"`
		Remote      bool   `json:"remote"`
		Confirm     string `json:"confirm"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Confirm != project {
		writeError(w, http.StatusBadRequest, "confirm_required", "restoring replaces live data", "confirm with the project name: "+project)
		return
	}
	if tok, _ := r.Context().Value(tokenKey{}).(store.Token); tok.Project != "" && body.FromProject != "" && body.FromProject != tok.Project {
		writeError(w, http.StatusForbidden, "forbidden", "this token is limited to project "+tok.Project, "")
		return
	}
	stream := newStream(w)
	err := s.deployer.Restore(context.WithoutCancel(r.Context()), project, body.Name,
		deploy.RestoreSource{Project: body.FromProject, Remote: body.Remote}, stream)
	stream.end(Result{OK: err == nil, Error: errString(err)})
}

// backupKayly snapshots Kayly's own database and key, e.g. before an upgrade.
func (s *Server) backupKayly(w http.ResponseWriter, r *http.Request) {
	b, err := s.deployer.SnapshotKayly(context.WithoutCancel(r.Context()), "manual")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) drillOffsite(w http.ResponseWriter, r *http.Request) {
	if err := s.deployer.Drill(context.WithoutCancel(r.Context())); err != nil {
		writeError(w, http.StatusBadGateway, "drill_failed", err.Error(), "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	path, err := s.deployer.BackupPath(r.PathValue("project"), r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("project")+"-"+r.PathValue("name")+`"`)
	http.ServeFile(w, r, path)
}

func releaseView(rel store.Release) ReleaseView {
	return ReleaseView{
		Seq: rel.Seq, Status: rel.Status, Error: rel.Error, CreatedAt: rel.CreatedAt.UTC().Format(time.RFC3339),
		Commit: rel.Commit.SHA, CommitMessage: rel.Commit.Message,
	}
}

// ReleaseView is a release as the API shows it.
type ReleaseView struct {
	Seq       int    `json:"seq"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
	// Commit and CommitMessage identify the source when the client knew it.
	Commit        string `json:"commit,omitempty"`
	CommitMessage string `json:"commit_message,omitempty"`
	// CanRollBack is true for a previous release whose image is still kept.
	CanRollBack bool `json:"can_roll_back,omitempty"`
}

// listServices is the live release's services with their CPU and memory, which cost a
// second to sample and so are not part of the project view.
func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	svcs, err := s.deployer.Services(r.Context(), p.Name, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, svcs)
}

// ServerView is the server Kayly runs on.
type ServerView struct {
	Version string `json:"version"`
	deploy.ServerStatus
}

func (s *Server) serverStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ServerView{Version: s.Version, ServerStatus: s.deployer.Server(r.Context())})
}

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	out, err := s.releaseViews(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) releaseViews(ctx context.Context, p store.Project) ([]ReleaseView, error) {
	rels, err := s.store.Releases(ctx, p.ID, 20)
	if err != nil {
		return nil, err
	}
	out := []ReleaseView{}
	for _, rel := range rels {
		v := releaseView(rel)
		v.CanRollBack = s.deployer.CanRollBack(ctx, rel)
		out = append(out, v)
	}
	return out, nil
}

// Result ends a deploy or rollback stream.
type Result struct {
	OK      bool   `json:"ok"`
	Release int    `json:"release,omitempty"`
	Error   string `json:"error,omitempty"`
}

// deploy receives the app source as a gzipped tar and streams the deploy.
func (s *Server) deploy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	dir, err := os.MkdirTemp(s.workDir, p.Name+"-")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer os.RemoveAll(dir)
	if err := extract(http.MaxBytesReader(w, r.Body, maxSourceBytes), dir); err != nil {
		writeError(w, http.StatusBadRequest, "bad_source", err.Error(), "")
		return
	}
	stream := newStream(w)
	// A client that disconnects must not abort a deploy halfway: it runs to the end.
	rel, err := s.deployer.Deploy(context.WithoutCancel(r.Context()), p.Name, dir, commitFrom(r), stream)
	stream.end(Result{OK: err == nil, Release: rel.Seq, Error: errString(err)})
}

// commitFrom reads the commit the CLI sends with a source upload. The client is trusted only
// as far as display goes: anything that does not look like a hash is dropped, and the
// message is a single, short line.
func commitFrom(r *http.Request) store.Commit {
	var c store.Commit
	if sha := r.Header.Get("X-Kayly-Commit"); commitRe.MatchString(sha) {
		c.SHA = sha
		if msg, err := url.PathUnescape(r.Header.Get("X-Kayly-Commit-Message")); err == nil {
			c.Message = oneLine(msg, 200)
		}
	}
	return c
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{7,64}(-dirty)?$`)

// oneLine cuts s at its first line break and at max runes.
func oneLine(s string, max int) string {
	s, _, _ = strings.Cut(s, "\n")
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

func (s *Server) releaseLog(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.Atoi(strings.TrimPrefix(r.PathValue("seq"), "r"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_release", "release must be a number such as 12 or r12", "")
		return
	}
	path, err := s.deployer.ReleaseLogPath(r.Context(), r.PathValue("project"), seq)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeFile(w, r, path)
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Release int `json:"release"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	stream := newStream(w)
	err := s.deployer.Rollback(context.WithoutCancel(r.Context()), r.PathValue("project"), body.Release, stream)
	stream.end(Result{OK: err == nil, Release: body.Release, Error: errString(err)})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	follow := q.Get("follow") == "1"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fw := &flushWriter{w: w}
	err := s.deployer.Logs(r.Context(), r.PathValue("project"), q.Get("service"), follow, tail, fw)
	if err != nil && !fw.wrote {
		s.fail(w, r, err)
	}
}

// EnvView lists the keys with a stored value. Values are never returned.
type EnvView struct {
	Keys []string `json:"keys"`
}

func (s *Server) listEnv(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	env, err := s.store.Env(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	writeJSON(w, http.StatusOK, EnvView{Keys: keys})
}

var envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func checkEnvKey(key string) error {
	if !envKeyRe.MatchString(key) {
		return fmt.Errorf("%q is not a valid env key", key)
	}
	return nil
}

func (s *Server) setEnv(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if err := checkEnvKey(key); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_key", err.Error(), "use A-Z, 0-9 and _")
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.SetEnv(r.Context(), p.ID, key, body.Value); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteEnv(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteEnv(r.Context(), p.ID, r.PathValue("key")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DomainView is a custom domain.
type DomainView struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	ds, err := s.store.Domains(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []DomainView{}
	for _, d := range ds {
		out = append(out, DomainView{Hostname: d.Hostname, Service: d.Service})
	}
	writeJSON(w, http.StatusOK, out)
}

var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func (s *Server) addDomain(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body DomainView
	if !readJSON(w, r, &body) {
		return
	}
	d, err := s.routeDomain(r.Context(), p, body)
	var ce *clientError
	switch {
	case errors.As(err, &ce):
		writeError(w, ce.status, ce.code, ce.Error(), "")
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, d)
	}
}

// clientError is a refused request: the caller asked for something invalid or taken.
type clientError struct {
	status int
	code   string
	msg    string
}

func (e *clientError) Error() string { return e.msg }

// routeDomain validates a custom domain, records it and reloads the proxy.
func (s *Server) routeDomain(ctx context.Context, p store.Project, d DomainView) (DomainView, error) {
	d.Hostname = strings.ToLower(strings.TrimSuffix(d.Hostname, "."))
	if !hostnameRe.MatchString(d.Hostname) || len(d.Hostname) > 253 {
		return d, &clientError{http.StatusBadRequest, "invalid_hostname", fmt.Sprintf("%q is not a hostname", d.Hostname)}
	}
	if !config.ValidName(d.Service) {
		return d, &clientError{http.StatusBadRequest, "invalid_service", fmt.Sprintf("%q is not a service name", d.Service)}
	}
	if err := s.store.AddDomain(ctx, store.Domain{Hostname: d.Hostname, ProjectID: p.ID, Service: d.Service}); err != nil {
		return d, &clientError{http.StatusConflict, "domain_taken", d.Hostname + " is already routed"}
	}
	return d, s.deployer.ReloadProxy(ctx)
}

func (s *Server) removeDomain(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	err := s.dropDomain(r.Context(), p, r.PathValue("hostname"))
	var ce *clientError
	switch {
	case errors.As(err, &ce):
		writeError(w, ce.status, ce.code, ce.Error(), "")
	case err != nil:
		s.fail(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// dropDomain removes a custom domain of p and reloads the proxy.
func (s *Server) dropDomain(ctx context.Context, p store.Project, host string) error {
	ds, err := s.store.Domains(ctx, p.ID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(ds, func(d store.Domain) bool { return d.Hostname == host }) {
		return &clientError{http.StatusNotFound, "not_found", host + " is not a domain of " + p.Name}
	}
	if err := s.store.RemoveDomain(ctx, host); err != nil {
		return err
	}
	return s.deployer.ReloadProxy(ctx)
}

// TokenView is an API token; Token is set only when it was just created.
type TokenView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Project   string `json:"project,omitempty"`
	Token     string `json:"token,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	LastUsed  string `json:"last_used_at,omitempty"`
}

func timeView(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.store.Tokens(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []TokenView{}
	for _, t := range toks {
		out = append(out, TokenView{ID: t.ID, Name: t.Name, Scope: t.Scope, Project: t.Project,
			ExpiresAt: timeView(t.ExpiresAt), CreatedAt: timeView(t.CreatedAt), LastUsed: timeView(t.LastUsedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Scope       string `json:"scope"`
		Project     string `json:"project"`
		ExpiresDays int    `json:"expires_days"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "a token needs a name", "name it after where it is used, e.g. laptop")
		return
	}
	if body.Scope == "" {
		body.Scope = store.ScopeAdmin
	}
	if !store.ValidScope(body.Scope) {
		writeError(w, http.StatusBadRequest, "invalid_scope", fmt.Sprintf("%q is not a token scope", body.Scope), "use read, deploy or admin")
		return
	}
	if body.ExpiresDays < 0 {
		writeError(w, http.StatusBadRequest, "invalid_expiry", "expires_days cannot be negative", "")
		return
	}
	n := store.NewToken{Name: body.Name, Scope: body.Scope}
	if body.Project != "" {
		p, err := s.store.Project(r.Context(), body.Project)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		n.ProjectID = p.ID
	}
	if body.ExpiresDays > 0 {
		n.ExpiresAt = time.Now().AddDate(0, 0, body.ExpiresDays)
	}
	tok, err := s.store.CreateToken(r.Context(), n)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "token create "+body.Name, body.Project)
	writeJSON(w, http.StatusCreated, TokenView{Name: body.Name, Scope: body.Scope, Project: body.Project, Token: tok, ExpiresAt: timeView(n.ExpiresAt)})
}

// AuditView is one entry of the audit log.
type AuditView struct {
	At      string `json:"at"`
	Token   string `json:"token"`
	Action  string `json:"action"`
	Project string `json:"project,omitempty"`
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(cmp.Or(r.URL.Query().Get("limit"), "100"))
	if err != nil || limit < 1 || limit > 1000 {
		writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 1000", "")
		return
	}
	entries, err := s.store.AuditLog(r.Context(), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []AuditView{}
	for _, e := range entries {
		out = append(out, AuditView{At: timeView(e.At), Token: e.Token, Action: e.Action, Project: e.Project})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "token id must be a number", "")
		return
	}
	if err := s.store.DeleteToken(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "token delete "+r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

// project loads the {project} path value, answering 404 if it does not exist.
func (s *Server) project(w http.ResponseWriter, r *http.Request) (store.Project, bool) {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), "a project is created by its first `kayly deploy`")
		return store.Project{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return store.Project{}, false
	}
	return p, true
}

// fail answers an error: 404 for missing things, 400 with the message otherwise. Kayly's
// errors are written for the person reading them, so the message is shown as is.
// NotificationsView says what is configured without returning a secret: the bot token and
// the webhook URL (which may carry one) are write-only.
type NotificationsView struct {
	Telegram     bool   `json:"telegram"`
	TelegramChat string `json:"telegram_chat,omitempty"`
	Webhook      bool   `json:"webhook"`
}

func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) {
	t, err := s.Notifier.Targets(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NotificationsView{
		Telegram: t.TelegramToken != "", TelegramChat: t.TelegramChat, Webhook: t.WebhookURL != "",
	})
}

// setNotifications changes the targets named in the body and keeps the rest; "" clears one.
func (s *Server) setNotifications(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TelegramToken *string `json:"telegram_token"`
		TelegramChat  *string `json:"telegram_chat"`
		WebhookURL    *string `json:"webhook_url"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	t, err := s.Notifier.Targets(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for dst, src := range map[*string]*string{&t.TelegramToken: body.TelegramToken, &t.TelegramChat: body.TelegramChat, &t.WebhookURL: body.WebhookURL} {
		if src != nil {
			*dst = *src
		}
	}
	if err := s.Notifier.SetTargets(r.Context(), t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_notifications", err.Error(), "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testNotifications(w http.ResponseWriter, r *http.Request) {
	if err := s.Notifier.Send(r.Context(), "✓ Kayly can reach you here."); err != nil {
		writeError(w, http.StatusBadGateway, "notify_failed", err.Error(), "check the bot token, the chat id and the webhook URL")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getOffsite(w http.ResponseWriter, r *http.Request) {
	st, err := s.Offsite.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// setOffsite stores the bucket. The answer carries the recovery key the first time and never
// again, so the owner must save it now.
func (s *Server) setOffsite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		Bucket    string `json:"bucket"`
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	key, err := s.Offsite.Configure(r.Context(), offsite.Config{
		Endpoint: body.Endpoint, Region: body.Region, Bucket: body.Bucket,
		AccessKey: body.AccessKey, SecretKey: body.SecretKey,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offsite", err.Error(), "check the endpoint, bucket and keys")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"recovery_key": key})
}

func (s *Server) syncOffsite(w http.ResponseWriter, r *http.Request) {
	if err := s.Offsite.Sync(context.WithoutCancel(r.Context())); err != nil {
		writeError(w, http.StatusBadGateway, "offsite_failed", err.Error(), "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), "")
		return
	}
	s.log.Warn("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusBadRequest, "failed", err.Error(), "")
}

// ErrorBody is the body of every non-2xx answer.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint,omitempty"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, msg, hint string) {
	var b ErrorBody
	b.Error.Code, b.Error.Message, b.Error.Hint = code, msg, hint
	writeJSON(w, status, b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body: "+err.Error(), "")
		return false
	}
	return true
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Event is one line of an NDJSON operation stream.
type Event struct {
	Log    string  `json:"log,omitempty"`
	Result *Result `json:"result,omitempty"`
}

// stream writes operation output as NDJSON events, flushing each one. Write errors (a
// disconnected client) are ignored so the operation still completes.
type stream struct {
	mu  sync.Mutex
	w   http.ResponseWriter
	enc *json.Encoder
}

func newStream(w http.ResponseWriter) *stream {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	return &stream{w: w, enc: json.NewEncoder(w)}
}

func (s *stream) Write(b []byte) (int, error) {
	s.send(Event{Log: string(b)})
	return len(b), nil
}

func (s *stream) end(r Result) { s.send(Event{Result: &r}) }

func (s *stream) send(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enc.Encode(e)
	http.NewResponseController(s.w).Flush()
}

// flushWriter flushes after every write, for streamed logs.
type flushWriter struct {
	w     http.ResponseWriter
	wrote bool
}

func (f *flushWriter) Write(b []byte) (int, error) {
	f.wrote = true
	n, err := f.w.Write(b)
	http.NewResponseController(f.w).Flush()
	return n, err
}
