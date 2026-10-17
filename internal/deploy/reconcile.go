package deploy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/proxy"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// Recover runs once when the daemon starts, before it accepts any deploy: releases
// interrupted by a stop or crash are marked failed. Call Reconcile afterwards (it may run
// alongside new deploys) to remove what they left behind.
func (d *Deployer) Recover(ctx context.Context) error {
	failed, err := d.store.FailInterrupted(ctx)
	if err != nil {
		return err
	}
	for _, r := range failed {
		d.log.Warn("release interrupted by a daemon stop, marked failed", "project_id", r.ProjectID, "release", r.Seq)
	}
	return nil
}

// Loop reconciles every interval until ctx is done.
func (d *Deployer) Loop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := d.Reconcile(ctx); err != nil {
				d.log.Error("reconcile", "err", err)
			}
		}
	}
}

// Reconcile converges actual state to desired state:
//  1. the proxy config is regenerated from the database (first, so it never points at a
//     container removed below);
//  2. for every project not busy with a deploy, the live release's containers are made to
//     run and every other container of the project is removed;
//  3. containers of projects that no longer exist are removed.
func (d *Deployer) Reconcile(ctx context.Context) error {
	d.proxyMu.Lock()
	err := d.loadProxy(ctx, nil)
	d.proxyMu.Unlock()
	if err != nil {
		return err
	}

	projects, err := d.store.Projects(ctx)
	if err != nil {
		return err
	}
	all, err := d.rt.List(ctx)
	if err != nil {
		return err
	}
	byProject := map[string][]Container{}
	for _, c := range all {
		byProject[c.Labels[LabelProject]] = append(byProject[c.Labels[LabelProject]], c)
	}

	var errs []error
	for _, p := range projects {
		have := byProject[p.Name]
		// Whatever happens below, these containers belong to a project that exists.
		delete(byProject, p.Name)
		l := d.projectLock(p.Name)
		if !l.TryLock() {
			continue // a deploy or rollback owns this project right now
		}
		if err := d.converge(ctx, p, have); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
		}
		l.Unlock()
	}
	for _, name := range slices.Sorted(maps.Keys(byProject)) {
		for _, c := range byProject[name] {
			d.log.Info("remove container of a deleted project", "container", c.Name)
			if err := d.rt.Remove(ctx, c.Name, d.opt.StopGrace); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// reconcileProject converges one project. The caller holds the project lock.
func (d *Deployer) reconcileProject(ctx context.Context, project string) error {
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return err
	}
	all, err := d.rt.List(ctx)
	if err != nil {
		return err
	}
	var mine []Container
	for _, c := range all {
		if c.Labels[LabelProject] == project {
			mine = append(mine, c)
		}
	}
	return d.converge(ctx, p, mine)
}

// converge makes the containers of p match its live release. The caller holds the project lock.
func (d *Deployer) converge(ctx context.Context, p store.Project, have []Container) error {
	want := map[string]Spec{}
	var cfg *config.Config
	if p.ActiveRelease != 0 {
		rel, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
		if err != nil {
			return err
		}
		if cfg, err = config.Parse(rel.Config); err != nil {
			return fmt.Errorf("r%d config: %w", rel.Seq, err)
		}
		for _, s := range d.serviceSpecs(p.Name, rel, cfg) {
			want[s.Name] = s
		}
	}

	var errs []error
	// Add-ons are not part of releases: keep them running, and never remove them here.
	if err := d.ensureAddons(ctx, p, false); err != nil {
		errs = append(errs, err)
	}
	alive := map[string]bool{}
	for _, c := range have {
		if c.Labels[LabelRole] == RoleAddon {
			continue
		}
		if _, ok := want[c.Name]; ok && (c.Running || c.Restarting) {
			alive[c.Name] = true
			continue
		}
		d.log.Info("remove container", "container", c.Name)
		if err := d.rt.Remove(ctx, c.Name, d.opt.StopGrace); err != nil {
			errs = append(errs, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		if alive[name] {
			continue
		}
		// Networks or volumes may be gone too, e.g. after a Docker reset.
		if err := d.ensureInfra(ctx, p.Name, cfg); err != nil {
			return errors.Join(append(errs, err)...)
		}
		d.log.Warn("start missing container of the live release", "container", name)
		if err := d.rt.Run(ctx, want[name]); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// loadProxy generates the proxy config from the live releases, with override replacing
// the live release of some projects, and loads it. The caller holds proxyMu.
func (d *Deployer) loadProxy(ctx context.Context, override map[int64]store.Release) error {
	projects, err := d.store.Projects(ctx)
	if err != nil {
		return err
	}
	routes := slices.Clone(d.opt.ExtraRoutes)
	for _, p := range projects {
		rel, ok := override[p.ID]
		if !ok {
			if p.ActiveRelease == 0 {
				continue
			}
			if rel, err = d.store.ReleaseByID(ctx, p.ActiveRelease); err != nil {
				return err
			}
		}
		cfg, err := config.Parse(rel.Config)
		if err != nil {
			return fmt.Errorf("%s r%d config: %w", p.Name, rel.Seq, err)
		}
		domains, err := d.store.Domains(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
			svc := cfg.Services[name]
			if svc.Port == 0 {
				continue
			}
			upstream := fmt.Sprintf("%s:%d", ContainerName(p.Name, name, rel.Seq), svc.Port)
			for _, h := range d.hosts(p.Name, name, domains) {
				routes = append(routes, proxy.Route{Host: h, Upstream: upstream})
			}
		}
	}
	cfg, err := proxy.Config(routes, d.opt.Proxy)
	if err != nil {
		return err
	}
	return d.proxy.Load(ctx, cfg)
}

// ReloadProxy regenerates and loads the proxy config, e.g. after a domain change.
func (d *Deployer) ReloadProxy(ctx context.Context) error {
	d.proxyMu.Lock()
	defer d.proxyMu.Unlock()
	return d.loadProxy(ctx, nil)
}
