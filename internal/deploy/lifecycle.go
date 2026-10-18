package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/kotlovyim-dev/kayly/internal/config"
)

// RestartService stops the live container of a service and starts it again from the same
// release. Unlike a deploy it has a short gap: the service is down while it restarts.
func (d *Deployer) RestartService(ctx context.Context, project, service string) error {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return err
	}
	if p.ActiveRelease == 0 {
		return fmt.Errorf("%s has no live release; run kayly deploy", project)
	}
	rel, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(rel.Config)
	if err != nil {
		return fmt.Errorf("r%d config: %w", rel.Seq, err)
	}
	name := ContainerName(project, service, rel.Seq)
	specs := d.serviceSpecs(project, rel, cfg)
	i := slices.IndexFunc(specs, func(s Spec) bool { return s.Name == name })
	if i < 0 {
		return fmt.Errorf("%s has no service %q", project, service)
	}
	spec := specs[i]
	if err := d.rt.Remove(ctx, name, d.opt.StopGrace); err != nil {
		return fmt.Errorf("stop %s: %w", name, err)
	}
	// If this fails the reconciler starts the container again within 30 s.
	if err := d.rt.Run(ctx, spec); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	return nil
}

// DeleteProject removes a project: its records, containers, add-ons, volumes and images.
// Local and off-site backups are kept. The records go first, so a crash part way leaves
// containers the reconciler removes, never a project without its records.
func (d *Deployer) DeleteProject(ctx context.Context, project string) error {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return err
	}
	releases, err := d.store.Releases(ctx, p.ID, 1<<20)
	if err != nil {
		return err
	}
	addons, err := d.store.Addons(ctx, p.ID)
	if err != nil {
		return err
	}
	volumes := map[string]bool{}
	for _, a := range addons {
		volumes[VolumeName(project, a.Kind)] = true
	}
	for _, r := range releases {
		cfg, err := config.Parse(r.Config)
		if err != nil {
			continue // an unreadable old release cannot name volumes; the rest are still removed
		}
		for _, s := range cfg.Services {
			for _, v := range s.Volumes {
				volumes[VolumeName(project, v.Name)] = true
			}
		}
	}

	if err := d.store.DeleteProject(ctx, p.ID); err != nil {
		return err
	}
	d.proxyMu.Lock()
	err = d.loadProxy(ctx, nil)
	d.proxyMu.Unlock()
	errs := []error{err}

	all, err := d.rt.List(ctx)
	errs = append(errs, err)
	for _, c := range all {
		if c.Labels[LabelProject] == project {
			errs = append(errs, d.rt.Remove(ctx, c.Name, d.opt.StopGrace))
		}
	}
	for v := range volumes {
		errs = append(errs, d.rt.RemoveVolume(ctx, v))
	}
	for _, r := range releases {
		if r.Image != "" {
			errs = append(errs, d.rt.RemoveImage(ctx, r.Image))
		}
	}
	return errors.Join(errs...)
}
