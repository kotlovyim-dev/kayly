// Package deploy runs the deploy pipeline, rollbacks and the reconciler (ARCHITECTURE.md §6).
//
// The rule every function here keeps: nothing before the proxy switch touches the live
// release, except stopping a service that opted out of overlap.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/proxy"
	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// Options configure a Deployer. Zero durations take the defaults.
type Options struct {
	// BaseDomain gives every service with a port the host <service>.<project>.<BaseDomain>,
	// e.g. "203-0-113-7.sslip.io", so HTTPS works before any DNS is set up.
	BaseDomain string
	Proxy      proxy.Options
	// ExtraRoutes are routed as well, e.g. the daemon's own UI.
	ExtraRoutes []proxy.Route
	// HealthTimeout bounds the wait for new containers to become healthy. Default 2m.
	HealthTimeout time.Duration
	// StopGrace is the time between SIGTERM and SIGKILL. Default 30s.
	StopGrace time.Duration
	// ReleaseTimeout bounds the release step. Default 30m.
	ReleaseTimeout time.Duration
	// KeepImages is how many past images stay for instant rollback. Default 3.
	KeepImages int
	// BackupDir holds database dumps, one directory per project. Empty disables backups.
	BackupDir string
	// LogDir keeps the output of every deploy, one file per release. Empty keeps none.
	LogDir string
	// DiskPath is a path on the server's data disk, watched for filling up. Empty disables it.
	DiskPath string
	// Offsite reads the owner's bucket for restores and drills. Nil without off-site backups.
	Offsite Offsite
	// KeyFile is the master key, which a Kayly backup carries with the database snapshot.
	KeyFile string
	// Notify tells the owner about a failure. Nil sends nothing.
	Notify func(ctx context.Context, text string)
}

// Deployer runs deploys, rollbacks and reconciliation. It is safe for concurrent use;
// operations on one project are serialized.
type Deployer struct {
	store   *store.Store
	rt      Runtime
	builder Builder
	proxy   Proxy
	opt     Options
	log     *slog.Logger

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// proxyMu is held from a proxy load until the database agrees with it, so a reconcile
	// can never load a config from a database that is about to change.
	proxyMu sync.Mutex

	// checkHealth probes addr ("ip:port"). Replaced in tests.
	checkHealth func(ctx context.Context, addr, path string) error
	// clock and diskUsage are replaced in tests.
	clock     func() time.Time
	diskUsage func(path string) (used, total uint64, err error)
	watch     watcher
	// settle is how long a service without a port must run before it counts as healthy.
	settle time.Duration
}

// New returns a Deployer.
func New(st *store.Store, rt Runtime, b Builder, p Proxy, opt Options, log *slog.Logger) *Deployer {
	if opt.HealthTimeout == 0 {
		opt.HealthTimeout = 2 * time.Minute
	}
	if opt.StopGrace == 0 {
		opt.StopGrace = 30 * time.Second
	}
	if opt.ReleaseTimeout == 0 {
		opt.ReleaseTimeout = 30 * time.Minute
	}
	if opt.KeepImages == 0 {
		opt.KeepImages = 3
	}
	return &Deployer{
		store: st, rt: rt, builder: b, proxy: p, opt: opt, log: log,
		locks:       map[string]*sync.Mutex{},
		checkHealth: probe,
		clock:       time.Now,
		diskUsage:   statfsUsage,
		settle:      10 * time.Second,
	}
}

// alert reports a failure to the owner. It must not block the caller's cleanup on a dead
// ctx, so it detaches from cancellation.
func (d *Deployer) alert(ctx context.Context, format string, args ...any) {
	if d.opt.Notify != nil {
		d.opt.Notify(context.WithoutCancel(ctx), fmt.Sprintf(format, args...))
	}
}

func (d *Deployer) projectLock(name string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	l, ok := d.locks[name]
	if !ok {
		l = &sync.Mutex{}
		d.locks[name] = l
	}
	return l
}

// Deploy builds the app in dir and, if every step succeeds, makes it the live release of
// project. On failure the previously live release keeps serving. Progress goes to w.
func (d *Deployer) Deploy(ctx context.Context, project, dir string, commit store.Commit, w io.Writer) (store.Release, error) {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	w = &syncWriter{w: w}

	p, err := d.store.Project(ctx, project)
	if err != nil {
		return store.Release{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		return store.Release{}, fmt.Errorf("read %s: %w", config.FileName, err)
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		return store.Release{}, err
	}
	rel, err := d.store.CreateRelease(ctx, p.ID, raw, commit)
	if err != nil {
		return store.Release{}, err
	}
	w, closeLog := d.keepLog(rel, w)
	defer closeLog()
	log := d.log.With("project", p.Name, "release", rel.Seq)
	log.Info("deploy started")

	build := func(image string) error {
		step(w, "build")
		err := d.builder.Build(ctx, BuildSpec{
			Dir: dir, Dockerfile: cfg.Build.Dockerfile, Target: cfg.Build.Target,
			Image: image, CacheKey: p.Name,
		}, w)
		if err != nil {
			return fmt.Errorf("build: %w", err)
		}
		return nil
	}
	if err := d.release(ctx, p, &rel, cfg, build, w); err != nil {
		d.abandon(ctx, p, rel, cfg, err, w)
		return rel, err
	}
	fmt.Fprintf(w, "\n✓ r%d is live\n", rel.Seq)
	log.Info("deploy finished")
	rel.Status = store.StatusLive
	return rel, nil
}

// abandon cleans up after a release that failed before going live: its containers go, it is
// recorded as failed, and the live release is restored if anything of it was stopped.
func (d *Deployer) abandon(ctx context.Context, p store.Project, rel store.Release, cfg *config.Config, cause error, w io.Writer) {
	// Cleanup must run even if the deploy was cancelled.
	ctx = context.WithoutCancel(ctx)
	log := d.log.With("project", p.Name, "release", rel.Seq)
	d.removeRelease(ctx, p.Name, rel.Seq, cfg)
	if err := d.store.Fail(ctx, rel.ID, cause.Error()); err != nil {
		log.Error("record failed release", "err", err)
	}
	// A non-overlapping service of the live release may have been stopped already.
	if err := d.reconcileProject(ctx, p.Name); err != nil {
		log.Error("restore live release", "err", err)
	}
	fmt.Fprintf(w, "\n✗ r%d failed: %v\n", rel.Seq, cause)
	log.Warn("release failed", "err", cause)
	msg := fmt.Sprintf("✗ %s: deploy r%d failed: %v", p.Name, rel.Seq, cause)
	if p.ActiveRelease != 0 {
		msg += "\nThe live release is untouched."
	}
	d.alert(ctx, "%s", msg)
}

// release takes a created release through every step: env, image (from makeImage), add-ons,
// backup, release step, start, health and switch.
func (d *Deployer) release(ctx context.Context, p store.Project, rel *store.Release, cfg *config.Config, makeImage func(image string) error, w io.Writer) error {
	// Env first: a missing value fails in a second, not after a build of several minutes.
	env, err := d.resolveEnv(ctx, p, cfg, w)
	if err != nil {
		return err
	}
	rel.Image = ImageName(p.Name, rel.Seq)
	if err := makeImage(rel.Image); err != nil {
		return err
	}
	if err := d.store.SetBuilt(ctx, rel.ID, rel.Image, env); err != nil {
		return err
	}
	rel.Env = env

	if err := d.ensureInfra(ctx, p.Name, cfg); err != nil {
		return err
	}
	if err := d.ensureAddons(ctx, p, true); err != nil {
		return err
	}
	if cfg.Release != nil {
		// A migration that goes wrong can always be undone from this dump.
		if d.hasAddon(ctx, p, "postgres") {
			step(w, "backup before the release step")
			b, err := d.backup(ctx, p, fmt.Sprintf("before-r%d", rel.Seq))
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s (%d KB)\n", b.Name, b.Size/1024)
		}
		step(w, "release: "+cfg.Release.Command)
		if err := d.runRelease(ctx, p.Name, *rel, cfg, w); err != nil {
			return fmt.Errorf("release step: %w", err)
		}
	}
	if err := d.store.SetStarting(ctx, rel.ID); err != nil {
		return err
	}
	return d.activate(ctx, p, *rel, cfg, w)
}

// Rollback makes an earlier release live again with its own image and env. It does not
// build and does not run the release step.
func (d *Deployer) Rollback(ctx context.Context, project string, seq int, w io.Writer) error {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	w = &syncWriter{w: w}

	p, err := d.store.Project(ctx, project)
	if err != nil {
		return err
	}
	rel, err := d.store.Release(ctx, p.ID, seq)
	if err != nil {
		return err
	}
	switch {
	case rel.ID == p.ActiveRelease:
		return fmt.Errorf("r%d is already live", seq)
	case rel.Status != store.StatusSuperseded:
		return fmt.Errorf("r%d is %s: only a release that was live can be rolled back to", seq, rel.Status)
	}
	if ok, err := d.rt.ImageExists(ctx, rel.Image); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("the image of r%d was pruned; deploy that commit again", seq)
	}
	cfg, err := config.Parse(rel.Config)
	if err != nil {
		return fmt.Errorf("r%d config: %w", seq, err)
	}
	if err := d.ensureInfra(ctx, p.Name, cfg); err != nil {
		return err
	}
	step(w, fmt.Sprintf("roll back to r%d", seq))
	if err := d.activate(ctx, p, rel, cfg, w); err != nil {
		cctx := context.WithoutCancel(ctx)
		d.removeRelease(cctx, p.Name, rel.Seq, cfg)
		if rerr := d.reconcileProject(cctx, p.Name); rerr != nil {
			d.log.Error("restore live release", "project", p.Name, "err", rerr)
		}
		d.alert(ctx, "✗ %s: rollback to r%d failed: %v", p.Name, seq, err)
		return err
	}
	fmt.Fprintf(w, "\n✓ r%d is live again\n", seq)
	return nil
}

// activate starts rel next to the live release, waits for health, switches the proxy,
// records the switch and drains the old release.
func (d *Deployer) activate(ctx context.Context, p store.Project, rel store.Release, cfg *config.Config, w io.Writer) error {
	var old *store.Release
	var oldCfg *config.Config
	if p.ActiveRelease != 0 {
		r, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
		if err != nil {
			return err
		}
		c, err := config.Parse(r.Config)
		if err != nil {
			return fmt.Errorf("live release r%d config: %w", r.Seq, err)
		}
		old, oldCfg = &r, c
	}

	// The only change to the live release before the switch, and only when asked for.
	if old != nil {
		for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
			if _, had := oldCfg.Services[name]; had && !cfg.Services[name].Overlaps() {
				fmt.Fprintf(w, "stopping %s of r%d first (overlap = false)\n", name, old.Seq)
				if err := d.rt.Remove(ctx, ContainerName(p.Name, name, old.Seq), d.opt.StopGrace); err != nil {
					return err
				}
			}
		}
	}

	step(w, "start")
	specs := d.serviceSpecs(p.Name, rel, cfg)
	for _, s := range specs {
		if err := d.rt.Run(ctx, s); err != nil {
			return fmt.Errorf("start %s: %w", s.Labels[LabelService], err)
		}
	}
	step(w, "health")
	if err := d.waitHealthy(ctx, p.Name, rel.Seq, cfg, w); err != nil {
		return err
	}

	step(w, "switch")
	if err := d.switchTo(ctx, p, rel); err != nil {
		return err
	}

	if old != nil {
		step(w, fmt.Sprintf("drain r%d", old.Seq))
		// After the switch a failure here leaves garbage, not an outage: the reconciler
		// removes whatever is left.
		d.removeRelease(ctx, p.Name, old.Seq, oldCfg)
	}
	d.prune(ctx, p)
	return nil
}

// switchTo points the proxy at rel and records rel as live.
func (d *Deployer) switchTo(ctx context.Context, p store.Project, rel store.Release) error {
	d.proxyMu.Lock()
	defer d.proxyMu.Unlock()
	if err := d.loadProxy(ctx, map[int64]store.Release{p.ID: rel}); err != nil {
		return err
	}
	if err := d.store.Activate(ctx, p.ID, rel.ID); err != nil {
		// The proxy already points at the new containers; point it back before they go.
		if perr := d.loadProxy(context.WithoutCancel(ctx), nil); perr != nil {
			d.log.Error("restore proxy config", "project", p.Name, "err", perr)
		}
		return err
	}
	return nil
}

func (d *Deployer) ensureInfra(ctx context.Context, project string, cfg *config.Config) error {
	if err := d.rt.EnsureNetwork(ctx, EdgeNetwork); err != nil {
		return err
	}
	if err := d.rt.EnsureNetwork(ctx, ProjectNetwork(project)); err != nil {
		return err
	}
	for _, s := range cfg.Services {
		for _, v := range s.Volumes {
			if err := d.rt.EnsureVolume(ctx, VolumeName(project, v.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Deployer) runRelease(ctx context.Context, project string, rel store.Release, cfg *config.Config, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, d.opt.ReleaseTimeout)
	defer cancel()
	cmd, err := shellquote.Split(cfg.Release.Command)
	if err != nil {
		return fmt.Errorf("parse command: %w", err)
	}
	name := ContainerName(project, config.ReleaseTarget, rel.Seq)
	spec := Spec{
		Name: name, Image: rel.Image, Cmd: cmd,
		Env:      rel.Env[config.ReleaseTarget],
		Labels:   labels(project, rel.Seq, RoleRelease, ""),
		Networks: []Network{{Name: ProjectNetwork(project)}},
		Init:     true,
	}
	if err := d.rt.Run(ctx, spec); err != nil {
		return err
	}
	defer d.rt.Remove(context.WithoutCancel(ctx), name, 0)
	if err := d.rt.Logs(ctx, name, true, 0, w); err != nil {
		return err
	}
	code, err := d.rt.Wait(ctx, name)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exited with code %d; the live release is untouched", code)
	}
	return nil
}

// serviceSpecs returns the containers of every service of rel, ordered by service name.
func (d *Deployer) serviceSpecs(project string, rel store.Release, cfg *config.Config) []Spec {
	var specs []Spec
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		s := cfg.Services[name]
		spec := Spec{
			Name:     ContainerName(project, name, rel.Seq),
			Image:    rel.Image,
			Env:      rel.Env[name],
			Labels:   labels(project, rel.Seq, RoleService, name),
			Networks: []Network{{Name: ProjectNetwork(project), Aliases: []string{name}}},
			ReadOnly: s.ReadOnly,
			Restart:  true,
			Init:     true,

			MemoryBytes: int64(s.Memory),
			CPUs:        s.CPUs,
		}
		if s.Command != "" {
			spec.Cmd, _ = shellquote.Split(s.Command) // config.Parse rejects commands that do not split
		}
		if s.Port != 0 {
			spec.Networks = append(spec.Networks, Network{Name: EdgeNetwork})
		}
		for _, v := range s.Volumes {
			spec.Mounts = append(spec.Mounts, Mount{Volume: VolumeName(project, v.Name), Path: v.Path})
		}
		for _, p := range s.Ports {
			spec.Ports = append(spec.Ports, Port{Host: p.Port, Container: p.Port, Proto: p.Proto})
		}
		specs = append(specs, spec)
	}
	return specs
}

// removeRelease removes every container of a release, logging failures.
func (d *Deployer) removeRelease(ctx context.Context, project string, seq int, cfg *config.Config) {
	var wg sync.WaitGroup
	for name := range cfg.Services {
		wg.Go(func() {
			if err := d.rt.Remove(ctx, ContainerName(project, name, seq), d.opt.StopGrace); err != nil {
				d.log.Error("remove container", "project", project, "release", seq, "service", name, "err", err)
			}
		})
	}
	wg.Wait()
}

// prune removes images beyond the newest KeepImages releases that went live, and images
// of failed releases. Failures are logged: a leftover image only costs disk.
func (d *Deployer) prune(ctx context.Context, p store.Project) {
	releases, err := d.store.Releases(ctx, p.ID, 100)
	if err != nil {
		d.log.Error("prune: list releases", "project", p.Name, "err", err)
		return
	}
	kept := 0
	for _, r := range releases {
		if r.Image == "" {
			continue
		}
		if (r.Status == store.StatusLive || r.Status == store.StatusSuperseded) && kept < d.opt.KeepImages {
			kept++
			continue
		}
		if err := d.rt.RemoveImage(ctx, r.Image); err != nil {
			d.log.Warn("prune image", "image", r.Image, "err", err)
		}
	}
}

// resolveEnv generates missing generated values, then resolves the env of every target.
func (d *Deployer) resolveEnv(ctx context.Context, p store.Project, cfg *config.Config, w io.Writer) (map[string]map[string]string, error) {
	stored, err := d.store.Env(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(cfg.Env)) {
		spec := cfg.Env[key].Generate
		if _, ok := stored[key]; ok || spec == "" {
			continue
		}
		v, err := secret.Generate(spec)
		if err != nil {
			return nil, fmt.Errorf("env.%s: %w", key, err)
		}
		if err := d.store.SetEnv(ctx, p.ID, key, v); err != nil {
			return nil, err
		}
		stored[key] = v
		fmt.Fprintf(w, "generated %s\n", key)
	}
	domains, err := d.store.Domains(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	refs, injected, err := d.addonEnv(ctx, p)
	if err != nil {
		return nil, err
	}
	for name, s := range cfg.Services {
		if hosts := d.hosts(p.Name, name, domains); s.Port != 0 && len(hosts) > 0 {
			refs[name+".url"] = "https://" + hosts[0]
		}
	}
	env, ignored, err := cfg.ResolveEnv(stored, refs, injected)
	if err != nil {
		var me *config.MissingError
		if errors.As(err, &me) {
			return nil, fmt.Errorf("%w; set them with `kayly env set KEY=value -p %s`", err, p.Name)
		}
		return nil, err
	}
	if len(ignored) > 0 {
		fmt.Fprintf(w, "warning: not declared in [env], not delivered: %s\n", strings.Join(ignored, ", "))
	}
	return env, nil
}

// hosts returns the hostnames of a service: custom domains first (oldest first), then the
// default host under BaseDomain.
func (d *Deployer) hosts(project, service string, domains []store.Domain) []string {
	var out []string
	for _, dm := range domains {
		if dm.Service == service {
			out = append(out, dm.Hostname)
		}
	}
	if d.opt.BaseDomain != "" {
		out = append(out, service+"."+project+"."+d.opt.BaseDomain)
	}
	return out
}

func step(w io.Writer, name string) { fmt.Fprintf(w, "\n==> %s\n", name) }

// syncWriter serializes writes from the per-service health checks into one deploy log.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(b)
}

// Logs writes the output of a service of the live release to w.
func (d *Deployer) Logs(ctx context.Context, project, service string, follow bool, tail int, w io.Writer) error {
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return err
	}
	if p.ActiveRelease == 0 {
		return fmt.Errorf("%s has no live release", project)
	}
	rel, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(rel.Config)
	if err != nil {
		return err
	}
	if _, ok := cfg.Services[service]; !ok {
		return fmt.Errorf("r%d has no service %q (services: %s)", rel.Seq, service,
			strings.Join(slices.Sorted(maps.Keys(cfg.Services)), ", "))
	}
	return d.rt.Logs(ctx, ContainerName(project, service, rel.Seq), follow, tail, w)
}

// Redeploy releases the live image again with the current env and add-ons, without a
// build: a new release that runs the release step and switches like any other. Use it
// after changing env values or adding an add-on.
func (d *Deployer) Redeploy(ctx context.Context, project string, w io.Writer) (store.Release, error) {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	w = &syncWriter{w: w}

	p, err := d.store.Project(ctx, project)
	if err != nil {
		return store.Release{}, err
	}
	if p.ActiveRelease == 0 {
		return store.Release{}, fmt.Errorf("%s has no live release to redeploy; run kayly deploy", project)
	}
	live, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
	if err != nil {
		return store.Release{}, err
	}
	cfg, err := config.Parse(live.Config)
	if err != nil {
		return store.Release{}, err
	}
	rel, err := d.store.CreateRelease(ctx, p.ID, live.Config, live.Commit)
	if err != nil {
		return store.Release{}, err
	}
	w, closeLog := d.keepLog(rel, w)
	defer closeLog()
	// Each release owns its tag, so pruning r(n) never removes the image of r(n+1).
	retag := func(image string) error {
		step(w, fmt.Sprintf("reuse the image of r%d", live.Seq))
		return d.rt.TagImage(ctx, live.Image, image)
	}
	if err := d.release(ctx, p, &rel, cfg, retag, w); err != nil {
		d.abandon(ctx, p, rel, cfg, err, w)
		return rel, err
	}
	fmt.Fprintf(w, "\n✓ r%d is live\n", rel.Seq)
	rel.Status = store.StatusLive
	return rel, nil
}

func (d *Deployer) hasAddon(ctx context.Context, p store.Project, kind string) bool {
	addons, err := d.store.Addons(ctx, p.ID)
	return err == nil && slices.ContainsFunc(addons, func(a store.Addon) bool { return a.Kind == kind })
}

// ServiceView is a service of the live release: where it is reachable and how it is doing.
type ServiceView struct {
	Name    string   `json:"name"`
	Port    int      `json:"port,omitempty"`
	Command string   `json:"command,omitempty"`
	URLs    []string `json:"urls"`
	// State is running, restarting, exited or missing (the reconciler is about to recreate it).
	State string `json:"state"`
	// Restarts is how often Docker has restarted the container after a crash.
	Restarts  int    `json:"restarts"`
	StartedAt string `json:"started_at,omitempty"`
	// MemoryLimit and CPUs are the caps from kayly.toml; zero means unlimited.
	MemoryLimit uint64  `json:"memory_limit,omitempty"`
	CPUs        float64 `json:"cpus,omitempty"`
	// Stats is set only when asked for, and only for a running container.
	Stats *Stats `json:"stats,omitempty"`
}

// Services lists the services of the live release, by name. With stats it also samples the
// CPU and memory of the running ones, which takes about a second (done in parallel).
func (d *Deployer) Services(ctx context.Context, project string, stats bool) ([]ServiceView, error) {
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	out := []ServiceView{}
	if p.ActiveRelease == 0 {
		return out, nil
	}
	rel, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(rel.Config)
	if err != nil {
		return nil, err
	}
	domains, err := d.store.Domains(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		s := cfg.Services[name]
		v := ServiceView{
			Name: name, Port: s.Port, Command: s.Command, URLs: []string{}, State: "missing",
			MemoryLimit: uint64(s.Memory), CPUs: s.CPUs,
		}
		if s.Port != 0 {
			for _, h := range d.hosts(project, name, domains) {
				v.URLs = append(v.URLs, "https://"+h)
			}
		}
		if c, err := d.rt.Inspect(ctx, ContainerName(project, name, rel.Seq)); err == nil {
			v.State, v.Restarts = containerState(c), c.RestartCount
			if !c.StartedAt.IsZero() {
				v.StartedAt = c.StartedAt.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, v)
	}
	if stats {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for i := range out {
			if out[i].State != "running" {
				continue
			}
			wg.Go(func() {
				if st, err := d.rt.Stats(sctx, ContainerName(project, out[i].Name, rel.Seq)); err == nil {
					out[i].Stats = &st
				}
			})
		}
		wg.Wait()
	}
	return out, nil
}

func containerState(c Container) string {
	switch {
	case c.Restarting:
		return "restarting"
	case c.Running:
		return "running"
	}
	return "exited"
}

// CanRollBack reports whether rel can become live again: it was live once and its image is
// still on disk (only the newest KeepImages are kept).
func (d *Deployer) CanRollBack(ctx context.Context, rel store.Release) bool {
	if rel.Status != store.StatusSuperseded || rel.Image == "" {
		return false
	}
	ok, err := d.rt.ImageExists(ctx, rel.Image)
	return err == nil && ok
}
