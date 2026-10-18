package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/gitsrc"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// MCP tools call the same functions as the HTTP handlers, under the same scope and project
// checks, and are audited the same way. No tool returns an env value or a credential.

const (
	// mcpWait is how long a tool waits for a deploy: to learn its release number, and with
	// `wait` for its outcome.
	mcpWait = 60 * time.Second
	// mcpMaxText caps the log text a tool returns, so one call cannot flood an agent's context.
	mcpMaxText = 32 << 10
)

func (s *Server) mcpHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.mcpServer(r.Context().Value(tokenKey{}).(store.Token))
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

// projectArg is embedded in the input of every tool that acts on one project.
type projectArg struct {
	Project string `json:"project" jsonschema:"the project name"`
}

func (a projectArg) projectName() string { return a.Project }

// noProject is embedded in the input of server-wide tools.
type noProject struct{}

func (noProject) projectName() string { return "" }

type toolInput interface{ projectName() string }

// tools registers the tools tok may call on one MCP server.
type tools struct {
	s   *Server
	tok store.Token
	srv *mcp.Server
}

// add registers a tool if tok's scope covers it. Every call is checked again (the project
// is only known from the arguments) and, unless it only reads, audited.
func add[In toolInput, Out any](t *tools, name, scope, desc string, destructive bool, h func(ctx context.Context, in In) (Out, error)) {
	if !t.tok.Allows(scope) {
		return
	}
	ann := &mcp.ToolAnnotations{ReadOnlyHint: scope == store.ScopeRead}
	if scope != store.ScopeRead {
		ann.DestructiveHint = &destructive
	}
	mcp.AddTool(t.srv, &mcp.Tool{Name: name, Description: desc, Annotations: ann},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			var zero Out
			project := in.projectName()
			if msg, _ := denial(t.tok, scope, project, project == ""); msg != "" {
				return nil, zero, errors.New(msg)
			}
			if scope != store.ScopeRead {
				if err := t.s.store.Audit(ctx, store.AuditEntry{Token: t.tok.Name, Action: "mcp " + name, Project: project}); err != nil {
					return nil, zero, err
				}
			}
			out, err := h(ctx, in)
			return nil, out, err
		})
}

// confirmed refuses a destructive call whose confirm is not the project's name, the rule the
// UI and CLI use.
func confirmed(confirm, project string) error {
	if confirm != project {
		return fmt.Errorf("this is destructive: pass confirm=%q to proceed", project)
	}
	return nil
}

// Tool inputs and outputs.
type (
	wait struct {
		Wait bool `json:"wait,omitempty" jsonschema:"block up to 60 s for the outcome instead of returning the release number at once"`
	}
	projectsOut struct {
		Projects []ProjectView `json:"projects"`
	}
	releasesOut struct {
		Releases []ReleaseView `json:"releases"`
	}
	releaseIn struct {
		projectArg
		Release int `json:"release" jsonschema:"release number, e.g. 12"`
	}
	releaseOut struct {
		Release ReleaseView `json:"release"`
		Log     string      `json:"log" jsonschema:"the end of the deploy output"`
	}
	logsIn struct {
		projectArg
		Service string `json:"service" jsonschema:"service name"`
		Tail    int    `json:"tail,omitempty" jsonschema:"number of lines, default 100, at most 1000"`
		Grep    string `json:"grep,omitempty" jsonschema:"keep only lines containing this text"`
	}
	logsOut struct {
		Logs string `json:"logs"`
	}
	envKey struct {
		Key string `json:"key"`
		// Set is true when a value is stored. Declared is true when the live kayly.toml lists the key.
		Set      bool     `json:"set"`
		Declared bool     `json:"declared"`
		To       []string `json:"to,omitempty" jsonschema:"services that receive it; empty means every service and the release step"`
	}
	envOut struct {
		Env []envKey `json:"env"`
	}
	domainsOut struct {
		Domains []DomainView `json:"domains"`
	}
	backupsIn struct {
		projectArg
		Remote bool `json:"remote,omitempty" jsonschema:"list the off-site bucket instead of the local backups"`
	}
	backupsOut struct {
		Backups []deploy.Backup `json:"backups"`
	}
	validateIn struct {
		noProject
		TOML string `json:"toml" jsonschema:"the contents of a kayly.toml"`
	}
	validateOut struct {
		Valid    bool     `json:"valid"`
		Error    string   `json:"error,omitempty" jsonschema:"what is wrong, with the line number"`
		Services []string `json:"services,omitempty"`
	}
	deployIn struct {
		projectArg
		wait
		Ref string `json:"ref,omitempty" jsonschema:"branch, tag or commit of the project's git source; default its branch"`
	}
	redeployIn struct {
		projectArg
		wait
	}
	rollbackIn struct {
		projectArg
		wait
		Release int `json:"release" jsonschema:"the earlier release to make live again"`
	}
	serviceIn struct {
		projectArg
		Service string `json:"service"`
	}
	setEnvIn struct {
		projectArg
		Key   string `json:"key" jsonschema:"A-Z, 0-9 and _"`
		Value string `json:"value"`
	}
	envKeyIn struct {
		projectArg
		Key string `json:"key"`
	}
	domainIn struct {
		projectArg
		Hostname string `json:"hostname"`
		Service  string `json:"service,omitempty" jsonschema:"the service that serves it"`
	}
	hostnameIn struct {
		projectArg
		Hostname string `json:"hostname"`
	}
	okOut struct {
		OK bool `json:"ok"`
	}
	addonIn struct {
		projectArg
		Kind string `json:"kind" jsonschema:"postgres or redis"`
	}
	addonsOut struct {
		Addons []deploy.AddonView `json:"addons" jsonschema:"connection URLs are never returned"`
	}
	removeAddonIn struct {
		projectArg
		Kind    string `json:"kind"`
		Confirm string `json:"confirm" jsonschema:"the project name"`
	}
	restoreIn struct {
		projectArg
		Name        string `json:"name" jsonschema:"a backup name from list_backups, or latest"`
		FromProject string `json:"from_project,omitempty" jsonschema:"restore another project's backup into this one"`
		Remote      bool   `json:"remote,omitempty"`
		Confirm     string `json:"confirm" jsonschema:"the project name"`
	}
	deleteProjectIn struct {
		projectArg
		Confirm string `json:"confirm" jsonschema:"the project name"`
	}
	// Outcome is what deploy, redeploy and rollback report.
	Outcome struct {
		Release int `json:"release,omitempty"`
		// Status is the release's: building, releasing, starting, live, failed or superseded.
		// "queued" means another deploy of the project holds the lock.
		Status string `json:"status"`
		Done   bool   `json:"done"`
		Error  string `json:"error,omitempty"`
	}
)

func (s *Server) mcpServer(tok store.Token) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "kayly", Version: s.Version}, nil)
	t := &tools{s: s, tok: tok, srv: srv}
	const read, dep, admin = store.ScopeRead, store.ScopeDeploy, store.ScopeAdmin

	add(t, "list_projects", read, "List the projects with their live release.", false,
		func(ctx context.Context, _ struct{ noProject }) (projectsOut, error) {
			ps, err := s.projectViews(ctx, tok)
			return projectsOut{ps}, err
		})
	add(t, "get_project", read, "One project: its live release, services (state, restarts, URLs), add-ons, domains, recent releases, and env keys the live release needs but lacks.", false,
		func(ctx context.Context, in projectArg) (ProjectDetail, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return ProjectDetail{}, err
			}
			d, err := s.projectDetail(ctx, p)
			d.Addons = redactAddons(store.Token{}, d.Addons)
			return d, err
		})
	add(t, "list_releases", read, "The newest releases of a project with status, error and commit.", false,
		func(ctx context.Context, in projectArg) (releasesOut, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return releasesOut{}, err
			}
			rs, err := s.releaseViews(ctx, p)
			return releasesOut{rs}, err
		})
	add(t, "get_release", read, "One release: status, error and the end of its deploy log.", false, s.mcpGetRelease)
	add(t, "get_logs", read, "Recent output of a service of the live release.", false, s.mcpLogs)
	add(t, "list_env", read, "Env keys of a project: whether each has a value, whether kayly.toml declares it and which services get it. Values are never returned.", false, s.mcpListEnv)
	add(t, "list_domains", read, "Custom domains of a project and the service each routes to.", false,
		func(ctx context.Context, in projectArg) (domainsOut, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return domainsOut{}, err
			}
			ds, err := s.store.Domains(ctx, p.ID)
			out := domainsOut{Domains: []DomainView{}}
			for _, d := range ds {
				out.Domains = append(out.Domains, DomainView{Hostname: d.Hostname, Service: d.Service})
			}
			return out, err
		})
	add(t, "list_backups", read, "Backups of a project, local or off-site.", false,
		func(ctx context.Context, in backupsIn) (backupsOut, error) {
			var bs []deploy.Backup
			var err error
			if in.Remote {
				bs, err = s.deployer.RemoteBackups(ctx, in.Project)
			} else {
				bs, err = s.deployer.Backups(in.Project)
			}
			return backupsOut{bs}, err
		})
	add(t, "server_status", read, "Disk, memory and versions of the server.", false,
		func(ctx context.Context, _ struct{ noProject }) (ServerView, error) {
			return ServerView{Version: s.Version, ServerStatus: s.deployer.Server(ctx)}, nil
		})
	add(t, "validate_config", read, "Check the text of a kayly.toml. Returns the first error with its line number.", false,
		func(_ context.Context, in validateIn) (validateOut, error) {
			cfg, err := config.Parse([]byte(in.TOML))
			if err != nil {
				return validateOut{Error: err.Error()}, nil
			}
			return validateOut{Valid: true, Services: slices.Sorted(maps.Keys(cfg.Services))}, nil
		})

	add(t, "deploy", dep, "Deploy a git ref of the project's git source. Returns the release number at once; with wait, blocks up to 60 s for the outcome, otherwise poll get_release.", false,
		func(ctx context.Context, in deployIn) (Outcome, error) {
			if in.Ref != "" {
				if err := gitsrc.ValidRef(in.Ref); err != nil {
					return Outcome{}, err
				}
			}
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return Outcome{}, err
			}
			return s.startRelease(ctx, p, in.Wait, func(ctx context.Context, notify func(int)) error {
				_, err := s.deployGit(deploy.WithReleaseNotify(ctx, notify), p, in.Ref, io.Discard)
				return err
			})
		})
	add(t, "redeploy", dep, "Release the live image again with the current env and add-ons, without a build. Use it after set_env or add_addon.", false,
		func(ctx context.Context, in redeployIn) (Outcome, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return Outcome{}, err
			}
			return s.startRelease(ctx, p, in.Wait, func(ctx context.Context, notify func(int)) error {
				_, err := s.deployer.Redeploy(deploy.WithReleaseNotify(ctx, notify), p.Name, io.Discard)
				return err
			})
		})
	add(t, "rollback", dep, "Make an earlier release live again with its own image and env; no build.", false,
		func(ctx context.Context, in rollbackIn) (Outcome, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return Outcome{}, err
			}
			return s.startRelease(ctx, p, in.Wait, func(ctx context.Context, notify func(int)) error {
				return s.deployer.Rollback(deploy.WithReleaseNotify(ctx, notify), p.Name, in.Release, io.Discard)
			})
		})
	add(t, "restart_service", dep, "Stop and start one service of the live release. The service is down for a moment.", false,
		func(ctx context.Context, in serviceIn) (okOut, error) {
			return okOut{true}, s.deployer.RestartService(context.WithoutCancel(ctx), in.Project, in.Service)
		})
	add(t, "set_env", dep, "Set an env value. It reaches the services on the next deploy or redeploy. Nothing reads values back.", false,
		func(ctx context.Context, in setEnvIn) (okOut, error) {
			if err := checkEnvKey(in.Key); err != nil {
				return okOut{}, err
			}
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return okOut{}, err
			}
			return okOut{true}, s.store.SetEnv(ctx, p.ID, in.Key, in.Value)
		})
	add(t, "delete_env", dep, "Delete an env value.", true,
		func(ctx context.Context, in envKeyIn) (okOut, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return okOut{}, err
			}
			return okOut{true}, s.store.DeleteEnv(ctx, p.ID, in.Key)
		})
	add(t, "add_domain", dep, "Route a custom domain to a service. Its DNS A record must point at the server.", false,
		func(ctx context.Context, in domainIn) (DomainView, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return DomainView{}, err
			}
			return s.routeDomain(ctx, p, DomainView{Hostname: in.Hostname, Service: in.Service})
		})
	add(t, "remove_domain", dep, "Remove a custom domain.", true,
		func(ctx context.Context, in hostnameIn) (okOut, error) {
			p, err := s.store.Project(ctx, in.Project)
			if err != nil {
				return okOut{}, err
			}
			return okOut{true}, s.dropDomain(ctx, p, strings.ToLower(in.Hostname))
		})
	add(t, "create_backup", dep, "Back up the project's add-ons and volumes now.", false,
		func(ctx context.Context, in projectArg) (backupsOut, error) {
			bs, err := s.deployer.BackupNow(context.WithoutCancel(ctx), in.Project)
			return backupsOut{bs}, err
		})

	add(t, "add_addon", admin, "Attach a Postgres or Redis add-on. Services receive its URL on the next deploy or redeploy.", false,
		func(ctx context.Context, in addonIn) (addonsOut, error) {
			if _, err := s.deployer.AddAddon(context.WithoutCancel(ctx), in.Project, in.Kind); err != nil {
				return addonsOut{}, err
			}
			as, err := s.deployer.Addons(ctx, in.Project)
			return addonsOut{redactAddons(store.Token{}, as)}, err
		})
	add(t, "remove_addon", admin, "Detach an add-on and delete its data. Backups are kept.", true,
		func(ctx context.Context, in removeAddonIn) (okOut, error) {
			if err := confirmed(in.Confirm, in.Project); err != nil {
				return okOut{}, err
			}
			return okOut{true}, s.deployer.RemoveAddon(ctx, in.Project, in.Kind)
		})
	add(t, "restore_backup", admin, "Replace live data with a backup. A backup of what is replaced is taken first.", true,
		func(ctx context.Context, in restoreIn) (okOut, error) {
			if err := confirmed(in.Confirm, in.Project); err != nil {
				return okOut{}, err
			}
			if tok.Project != "" && in.FromProject != "" && in.FromProject != tok.Project {
				return okOut{}, errors.New("this token is limited to project " + tok.Project)
			}
			err := s.deployer.Restore(context.WithoutCancel(ctx), in.Project, in.Name,
				deploy.RestoreSource{Project: in.FromProject, Remote: in.Remote}, io.Discard)
			return okOut{err == nil}, err
		})
	add(t, "delete_project", admin, "Delete a project with its containers, add-ons and volumes. Backups are kept.", true,
		func(ctx context.Context, in deleteProjectIn) (okOut, error) {
			if err := confirmed(in.Confirm, in.Project); err != nil {
				return okOut{}, err
			}
			return okOut{true}, s.deployer.DeleteProject(context.WithoutCancel(ctx), in.Project)
		})
	return srv
}

func (s *Server) mcpGetRelease(ctx context.Context, in releaseIn) (releaseOut, error) {
	p, err := s.store.Project(ctx, in.Project)
	if err != nil {
		return releaseOut{}, err
	}
	rel, err := s.store.Release(ctx, p.ID, in.Release)
	if err != nil {
		return releaseOut{}, err
	}
	out := releaseOut{Release: releaseView(rel)}
	if path, err := s.deployer.ReleaseLogPath(ctx, in.Project, in.Release); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			out.Log = tailText(b, mcpMaxText)
		}
	}
	return out, nil
}

func (s *Server) mcpLogs(ctx context.Context, in logsIn) (logsOut, error) {
	tail := min(max(in.Tail, 1), 1000)
	if in.Tail == 0 {
		tail = 100
	}
	fetch := tail
	if in.Grep != "" {
		fetch = 1000 // search a wide window, then keep the newest matches
	}
	var buf bytes.Buffer
	if err := s.deployer.Logs(ctx, in.Project, in.Service, false, fetch, &buf); err != nil {
		return logsOut{}, err
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if in.Grep != "" {
		lines = slices.DeleteFunc(lines, func(l string) bool { return !strings.Contains(l, in.Grep) })
		lines = lines[max(len(lines)-tail, 0):]
	}
	return logsOut{tailText([]byte(strings.Join(lines, "\n")), mcpMaxText)}, nil
}

// tailText returns the last max bytes of b, starting at a line.
func tailText(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	b = b[len(b)-max:]
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[i+1:]
	}
	return "…(earlier output cut)\n" + string(b)
}

func (s *Server) mcpListEnv(ctx context.Context, in projectArg) (envOut, error) {
	p, err := s.store.Project(ctx, in.Project)
	if err != nil {
		return envOut{}, err
	}
	stored, err := s.store.Env(ctx, p.ID)
	if err != nil {
		return envOut{}, err
	}
	declared := map[string]config.EnvVar{}
	if p.ActiveRelease != 0 {
		rel, err := s.store.ReleaseByID(ctx, p.ActiveRelease)
		if err != nil {
			return envOut{}, err
		}
		if cfg, err := config.Parse(rel.Config); err == nil {
			declared = cfg.Env
		}
	}
	keys := map[string]bool{}
	for k := range stored {
		keys[k] = true
	}
	for k := range declared {
		keys[k] = true
	}
	out := envOut{Env: []envKey{}}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		spec, isDeclared := declared[k]
		_, set := stored[k]
		out.Env = append(out.Env, envKey{Key: k, Set: set, Declared: isDeclared, To: spec.To})
	}
	return out, nil
}

// startRelease runs a deploy, redeploy or rollback of p in the background and reports the
// release it creates as soon as it is known. With wait it also holds for the outcome. The
// pipeline outlives the call either way.
func (s *Server) startRelease(ctx context.Context, p store.Project, wait bool, run func(ctx context.Context, notify func(seq int)) error) (Outcome, error) {
	seqs := make(chan int, 1)
	done := make(chan error, 1)
	notify := func(seq int) {
		select {
		case seqs <- seq:
		default:
		}
	}
	go func() { done <- run(context.WithoutCancel(ctx), notify) }()

	var seq int
	var runErr error
	finished := false
	timeout := time.After(mcpWait)
	select {
	case seq = <-seqs:
	case runErr = <-done:
		finished = true
		select {
		case seq = <-seqs:
		default:
		}
	case <-timeout:
		return Outcome{Status: "queued"}, nil
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
	if wait && !finished {
		select {
		case runErr = <-done:
			finished = true
		case <-timeout:
		case <-ctx.Done():
		}
	}

	out := Outcome{Release: seq, Done: finished, Status: "building"}
	if seq != 0 {
		if rel, err := s.store.Release(ctx, p.ID, seq); err == nil {
			out.Status, out.Error = rel.Status, rel.Error
		}
	}
	if runErr != nil {
		out.Status = "failed"
		if out.Error == "" {
			out.Error = runErr.Error()
		}
	}
	return out, nil
}
