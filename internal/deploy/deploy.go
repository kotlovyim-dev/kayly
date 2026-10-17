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
		settle:      10 * time.Second,
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
func (d *Deployer) Deploy(ctx context.Context, project, dir string, w io.Writer) (store.Release, error) {
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
	if cfg.Addons.Postgres != nil {
		return store.Release{}, errors.New("addons.postgres: not supported yet")
	}
	rel, err := d.store.CreateRelease(ctx, p.ID, raw)
	if err != nil {
		return store.Release{}, err
	}
	log := d.log.With("project", p.Name, "release", rel.Seq)
	log.Info("deploy started")

	if err := d.deploy(ctx, p, &rel, cfg, dir, w); err != nil {
		// Cleanup must run even if the deploy was cancelled.
		cctx := context.WithoutCancel(ctx)
		d.removeRelease(cctx, p.Name, rel.Seq, cfg)
		if ferr := d.store.Fail(cctx, rel.ID, err.Error()); ferr != nil {
			log.Error("record failed release", "err", ferr)
		}
		// A non-overlapping service of the live release may have been stopped already.
		if rerr := d.reconcileProject(cctx, p.Name); rerr != nil {
			log.Error("restore live release", "err", rerr)
		}
		fmt.Fprintf(w, "\n✗ r%d failed: %v\n", rel.Seq, err)
		log.Warn("deploy failed", "err", err)
		return rel, err
	}
	fmt.Fprintf(w, "\n✓ r%d is live\n", rel.Seq)
	log.Info("deploy finished")
	rel.Status = store.StatusLive
	return rel, nil
}

func (d *Deployer) deploy(ctx context.Context, p store.Project, rel *store.Release, cfg *config.Config, dir string, w io.Writer) error {
	step(w, "build")
	rel.Image = ImageName(p.Name, rel.Seq)
	err := d.builder.Build(ctx, BuildSpec{
		Dir: dir, Dockerfile: cfg.Build.Dockerfile, Target: cfg.Build.Target,
		Image: rel.Image, CacheKey: p.Name,
	}, w)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}

	env, err := d.resolveEnv(ctx, p, cfg, w)
	if err != nil {
		return err
	}
	if err := d.store.SetBuilt(ctx, rel.ID, rel.Image, env); err != nil {
		return err
	}
	rel.Env = env

	if err := d.ensureInfra(ctx, p.Name, cfg); err != nil {
		return err
	}
	if cfg.Release != nil {
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
	refs := map[string]string{}
	for name, s := range cfg.Services {
		if hosts := d.hosts(p.Name, name, domains); s.Port != 0 && len(hosts) > 0 {
			refs[name+".url"] = "https://" + hosts[0]
		}
	}
	env, ignored, err := cfg.ResolveEnv(stored, refs)
	if err != nil {
		var me *config.MissingError
		if errors.As(err, &me) {
			return nil, fmt.Errorf("%w; set them with `kayly env set %s KEY=value`", err, p.Name)
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
