// Package api serves Kayly's HTTP API, used by the CLI, the UI and agents.
//
// Errors have one shape: {"error": {"code", "message", "hint"}}. Long operations (deploy,
// rollback) answer 200 with an NDJSON stream of {"log": "..."} events ending in one
// {"result": {...}} event, because their outcome is only known at the end.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// Server holds the API's dependencies.
type Server struct {
	store    *store.Store
	deployer *deploy.Deployer
	// workDir is where uploaded sources are unpacked for a build.
	workDir string
	log     *slog.Logger
}

// New returns a Server. workDir must exist.
func New(st *store.Store, d *deploy.Deployer, workDir string, log *slog.Logger) *Server {
	return &Server{store: st, deployer: d, workDir: workDir, log: log}
}

// Handler returns the API. A trusted handler (the root-only admin socket) needs no token
// and may manage tokens; an untrusted one (behind the proxy) requires a bearer token.
func (s *Server) Handler(trusted bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", s.listProjects)
	mux.HandleFunc("POST /api/v1/projects", s.createProject)
	mux.HandleFunc("GET /api/v1/projects/{project}/releases", s.listReleases)
	mux.HandleFunc("POST /api/v1/projects/{project}/deploys", s.deploy)
	mux.HandleFunc("POST /api/v1/projects/{project}/rollback", s.rollback)
	mux.HandleFunc("GET /api/v1/projects/{project}/logs", s.logs)
	mux.HandleFunc("GET /api/v1/projects/{project}/env", s.listEnv)
	mux.HandleFunc("PUT /api/v1/projects/{project}/env/{key}", s.setEnv)
	mux.HandleFunc("DELETE /api/v1/projects/{project}/env/{key}", s.deleteEnv)
	mux.HandleFunc("GET /api/v1/projects/{project}/domains", s.listDomains)
	mux.HandleFunc("POST /api/v1/projects/{project}/domains", s.addDomain)
	mux.HandleFunc("DELETE /api/v1/projects/{project}/domains/{hostname}", s.removeDomain)
	if trusted {
		mux.HandleFunc("GET /api/v1/tokens", s.listTokens)
		mux.HandleFunc("POST /api/v1/tokens", s.createToken)
		mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.deleteToken)
		return mux
	}
	return s.requireToken(mux)
}

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok {
			valid, err := s.store.CheckToken(r.Context(), tok)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			ok = valid
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token",
				"create one on the server with `kayly token create <name>`, then `kayly login`")
			return
		}
		next.ServeHTTP(w, r)
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
	projects, err := s.store.Projects(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []ProjectView{}
	for _, p := range projects {
		v := ProjectView{Name: p.Name, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}
		if p.ActiveRelease != 0 {
			rel, err := s.store.ReleaseByID(r.Context(), p.ActiveRelease)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			v.LiveSeq, v.LiveSince = rel.Seq, rel.CreatedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
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

// ReleaseView is a release as the API shows it.
type ReleaseView struct {
	Seq       int    `json:"seq"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	rels, err := s.store.Releases(r.Context(), p.ID, 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []ReleaseView{}
	for _, rel := range rels {
		out = append(out, ReleaseView{Seq: rel.Seq, Status: rel.Status, Error: rel.Error, CreatedAt: rel.CreatedAt.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, out)
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
	rel, err := s.deployer.Deploy(context.WithoutCancel(r.Context()), p.Name, dir, stream)
	stream.end(Result{OK: err == nil, Release: rel.Seq, Error: errString(err)})
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

func (s *Server) setEnv(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if !envKeyRe.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid_key", fmt.Sprintf("%q is not a valid env key", key), "use A-Z, 0-9 and _")
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
	body.Hostname = strings.ToLower(strings.TrimSuffix(body.Hostname, "."))
	if !hostnameRe.MatchString(body.Hostname) || len(body.Hostname) > 253 {
		writeError(w, http.StatusBadRequest, "invalid_hostname", fmt.Sprintf("%q is not a hostname", body.Hostname), "")
		return
	}
	if !config.ValidName(body.Service) {
		writeError(w, http.StatusBadRequest, "invalid_service", fmt.Sprintf("%q is not a service name", body.Service), "")
		return
	}
	if err := s.store.AddDomain(r.Context(), store.Domain{Hostname: body.Hostname, ProjectID: p.ID, Service: body.Service}); err != nil {
		writeError(w, http.StatusConflict, "domain_taken", body.Hostname+" is already routed", "")
		return
	}
	if err := s.deployer.ReloadProxy(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) removeDomain(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	host := r.PathValue("hostname")
	ds, err := s.store.Domains(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !slices.ContainsFunc(ds, func(d store.Domain) bool { return d.Hostname == host }) {
		writeError(w, http.StatusNotFound, "not_found", host+" is not a domain of "+p.Name, "")
		return
	}
	if err := s.store.RemoveDomain(r.Context(), host); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deployer.ReloadProxy(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TokenView is an API token; Token is set only when it was just created.
type TokenView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.store.Tokens(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []TokenView{}
	for _, t := range toks {
		out = append(out, TokenView{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "a token needs a name", "name it after where it is used, e.g. laptop")
		return
	}
	tok, err := s.store.CreateToken(r.Context(), body.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, TokenView{Name: body.Name, Token: tok})
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
	w.WriteHeader(http.StatusNoContent)
}

// project loads the {project} path value, answering 404 if it does not exist.
func (s *Server) project(w http.ResponseWriter, r *http.Request) (store.Project, bool) {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		s.fail(w, r, err)
		return store.Project{}, false
	}
	return p, true
}

// fail answers an error: 404 for missing things, 400 with the message otherwise. Kayly's
// errors are written for the person reading them, so the message is shown as is.
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
