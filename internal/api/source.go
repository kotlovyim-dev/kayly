package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/kotlovyim-dev/kayly/internal/gitsrc"
	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// SourceView is the git repository a project follows. The webhook secret is returned only
// by the call that created it: GitHub needs it once.
type SourceView struct {
	URL           string `json:"url"`
	Branch        string `json:"branch"`
	PublicKey     string `json:"public_key"`
	WebhookURL    string `json:"webhook_url"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

func (s *Server) sourceView(project string, src store.Source) (SourceView, error) {
	pub, err := gitsrc.PublicKey(src.DeployKey)
	if err != nil {
		return SourceView{}, err
	}
	return SourceView{URL: src.URL, Branch: src.Branch, PublicKey: pub, WebhookURL: s.PublicURL + "/api/v1/hooks/github/" + project}, nil
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	src, err := s.store.Source(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if src.URL == "" {
		writeError(w, http.StatusNotFound, "no_source", p.Name+" does not follow a git repository", "set one with `kayly source set <git-url>`")
		return
	}
	v, err := s.sourceView(p.Name, src)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// setSource points a project at a repository. The deploy key and webhook secret are made
// the first time and kept when only the URL or branch changes.
func (s *Server) setSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		URL    string `json:"url"`
		Branch string `json:"branch"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Branch == "" {
		body.Branch = "main"
	}
	if err := gitsrc.ValidURL(body.URL); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_url", err.Error(), "use the SSH form, e.g. git@github.com:owner/repo.git")
		return
	}
	if err := gitsrc.ValidRef(body.Branch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_branch", err.Error(), "")
		return
	}
	src, err := s.store.Source(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var revealed string
	if src.URL == "" {
		if src.DeployKey, _, err = gitsrc.GenerateKey("kayly:" + p.Name); err != nil {
			s.fail(w, r, err)
			return
		}
		if src.WebhookSecret, err = secret.Generate("hex:32"); err != nil {
			s.fail(w, r, err)
			return
		}
		revealed = src.WebhookSecret
	}
	src.URL, src.Branch = body.URL, body.Branch
	if err := s.store.SetSource(r.Context(), p.ID, src); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.sourceView(p.Name, src)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.WebhookSecret = revealed
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	if err := s.store.ClearSource(r.Context(), p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deployGit deploys ref (the followed branch if empty) of the project's repository.
func (s *Server) deployGit(ctx context.Context, p store.Project, ref string, w io.Writer) (store.Release, error) {
	src, err := s.store.Source(ctx, p.ID)
	if err != nil {
		return store.Release{}, err
	}
	switch {
	case src.URL == "":
		return store.Release{}, fmt.Errorf("%s does not follow a git repository; set one with `kayly source set <git-url>`", p.Name)
	case s.Git == nil:
		return store.Release{}, errors.New("this daemon was started without git support")
	}
	if ref == "" {
		ref = src.Branch
	}
	dir, err := os.MkdirTemp(s.workDir, p.Name+"-")
	if err != nil {
		return store.Release{}, err
	}
	defer os.RemoveAll(dir)
	fmt.Fprintf(w, "==> fetch %s %s\n", src.URL, ref)
	commit, err := s.Git.Fetch(ctx, src.URL, ref, src.DeployKey, dir)
	if err != nil {
		return store.Release{}, fmt.Errorf("fetch: %w", err)
	}
	fmt.Fprintf(w, "%s %s\n", commit.SHA[:12], commit.Message)
	return s.deployer.Deploy(ctx, p.Name, dir, commit, w)
}

func (s *Server) deployFromGit(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Ref string `json:"ref"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &body) {
		return
	}
	if body.Ref != "" {
		if err := gitsrc.ValidRef(body.Ref); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_ref", err.Error(), "")
			return
		}
	}
	stream := newStream(w)
	rel, err := s.deployGit(context.WithoutCancel(r.Context()), p, body.Ref, stream)
	stream.end(Result{OK: err == nil, Release: rel.Seq, Error: errString(err)})
}

// maxHookBytes bounds a webhook body; GitHub caps its payloads at 25 MB but a push event
// is a few KB.
const maxHookBytes = 1 << 20

// githubHook deploys the commit of a signed push event to the followed branch. GitHub
// waits ten seconds for an answer, so the deploy runs on after it.
func (s *Server) githubHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "webhook body too large", "")
		return
	}
	// Unknown projects and projects without a source look the same as a bad signature.
	deny := func() { writeError(w, http.StatusUnauthorized, "unauthorized", "bad webhook signature", "") }
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		deny()
		return
	}
	src, err := s.store.Source(r.Context(), p.ID)
	if err != nil || src.URL == "" || !validSignature(src.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		deny()
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		w.WriteHeader(http.StatusNoContent)
		return
	case "push":
	default:
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var push struct {
		Ref     string `json:"ref"`
		After   string `json:"after"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(body, &push); err != nil {
		writeError(w, http.StatusBadRequest, "bad_payload", "not a push event", "")
		return
	}
	if push.Deleted || push.Ref != "refs/heads/"+src.Branch {
		w.WriteHeader(http.StatusNoContent) // another branch: nothing to do
		return
	}
	if !fullSHA.MatchString(push.After) {
		writeError(w, http.StatusBadRequest, "bad_payload", "push event without a commit", "")
		return
	}
	if err := s.store.Audit(r.Context(), store.AuditEntry{Token: "github", Action: "push " + push.After[:12], Project: p.Name}); err != nil {
		s.fail(w, r, err)
		return
	}
	go func() {
		ctx := context.Background()
		if rel, err := s.deployGit(ctx, p, push.After, io.Discard); err != nil {
			s.log.Warn("deploy from push failed", "project", p.Name, "commit", push.After, "err", err)
			// A failure after the release was created is alerted by the pipeline; one before
			// it (fetch, config) has no release, so say it here.
			if s.Notifier != nil && rel.Seq == 0 {
				s.Notifier.Notify(ctx, fmt.Sprintf("%s: deploy of %s from GitHub failed: %v", p.Name, push.After[:12], err))
			}
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// validSignature checks GitHub's "sha256=<hex>" HMAC of the body, in constant time.
func validSignature(secret string, body []byte, header string) bool {
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok || secret == "" {
		return false
	}
	want := hmac.New(sha256.New, []byte(secret))
	want.Write(body)
	sig, err := hex.DecodeString(got)
	return err == nil && hmac.Equal(sig, want.Sum(nil))
}
