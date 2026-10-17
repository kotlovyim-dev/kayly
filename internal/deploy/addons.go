package deploy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// addonKind describes one kind of add-on Kayly can attach to a project.
type addonKind struct {
	image     string // pinned; recorded per add-on, so a Kayly upgrade never changes a database
	port      int
	dataPath  string
	envVar    string // injected into the project's services, e.g. DATABASE_URL
	spec      func(project string, a store.Addon) Spec
	stopGrace time.Duration
}

// addonKinds lists the add-ons Kayly can attach. Their fields must match config.AddonFields.
var addonKinds = map[string]addonKind{
	"postgres": {
		image:     "postgres:18.6-alpine@sha256:6c538e7206ea40ff740ef27883529390a690b6ead6ba96b44c67a9f7c638e8fd",
		port:      5432,
		dataPath:  "/var/lib/postgresql",
		envVar:    "DATABASE_URL",
		stopGrace: 60 * time.Second,
		spec: func(project string, a store.Addon) Spec {
			return Spec{
				Env: map[string]string{
					"POSTGRES_USER":     "postgres",
					"POSTGRES_PASSWORD": a.Password,
					"POSTGRES_DB":       pgDatabase(project),
					// Code-point collation: never drifts with libc or ICU upgrades.
					"POSTGRES_INITDB_ARGS": "--encoding=UTF8 --locale-provider=builtin --builtin-locale=C.UTF-8",
				},
				Health: []string{"CMD-SHELL", "pg_isready -U postgres -d " + pgDatabase(project)},
				// The entrypoint initializes the data directory as root, then drops to postgres.
				CapAdd: []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"},
			}
		},
	},
	"redis": {
		image:     "valkey/valkey:9.1.2-alpine@sha256:48332870af354a799964c0012ae1194a0bf2bf894eb508f945810596dc2d8d11",
		port:      6379,
		dataPath:  "/data",
		envVar:    "REDIS_URL",
		stopGrace: 30 * time.Second,
		spec: func(_ string, a store.Addon) Spec {
			return Spec{
				Cmd:            []string{"valkey-server", "--appendonly", "yes", "--requirepass", a.Password},
				KeepEntrypoint: true, // it fixes /data ownership and drops to the valkey user
				Health:         []string{"CMD-SHELL", "valkey-cli --no-auth-warning -a " + a.Password + " ping | grep -q PONG"},
				CapAdd:         []string{"CHOWN", "SETGID", "SETUID"},
			}
		},
	},
}

// AddonKindNames returns the kinds of add-on Kayly supports, sorted.
func AddonKindNames() []string { return slices.Sorted(maps.Keys(addonKinds)) }

// AddonContainer is the container of a project's add-on.
func AddonContainer(project, kind string) string { return project + "_" + kind }

// pgDatabase is the database the Postgres add-on creates: the project name as an identifier.
func pgDatabase(project string) string { return strings.ReplaceAll(project, "-", "_") }

// addonSpec is the full container spec of an add-on.
func addonSpec(project string, a store.Addon) Spec {
	k := addonKinds[a.Kind]
	s := k.spec(project, a)
	s.Name = AddonContainer(project, a.Kind)
	s.Image = a.Image
	s.Labels = labels(project, 0, RoleAddon, a.Kind)
	// Services reach the add-on by its kind: postgres:5432, redis:6379.
	s.Networks = []Network{{Name: ProjectNetwork(project), Aliases: []string{a.Kind}}}
	s.Mounts = []Mount{{Volume: VolumeName(project, a.Kind), Path: k.dataPath}}
	s.Restart = true
	return s
}

// addonFields returns the ${<kind>.<field>} values of an add-on (config.AddonFields).
func addonFields(project string, a store.Addon) map[string]string {
	k := addonKinds[a.Kind]
	f := map[string]string{
		"host":     a.Kind,
		"port":     strconv.Itoa(k.port),
		"password": a.Password,
	}
	u := url.URL{Host: a.Kind + ":" + f["port"]}
	switch a.Kind {
	case "postgres":
		f["user"], f["db"] = "postgres", pgDatabase(project)
		u.Scheme, u.User, u.Path = "postgres", url.UserPassword("postgres", a.Password), "/"+f["db"]
	case "redis":
		u.Scheme, u.User = "redis", url.UserPassword("default", a.Password)
	}
	f["url"] = u.String()
	return f
}

// addonEnv returns the template references and injected variables of p's add-ons.
func (d *Deployer) addonEnv(ctx context.Context, p store.Project) (refs, injected map[string]string, err error) {
	addons, err := d.store.Addons(ctx, p.ID)
	if err != nil {
		return nil, nil, err
	}
	refs, injected = map[string]string{}, map[string]string{}
	for _, a := range addons {
		f := addonFields(p.Name, a)
		for k, v := range f {
			refs[a.Kind+"."+k] = v
		}
		injected[addonKinds[a.Kind].envVar] = f["url"]
	}
	return refs, injected, nil
}

// AddAddon attaches a new add-on of kind to project and starts it. The project's services
// get its variable (DATABASE_URL, REDIS_URL) on their next deploy or redeploy.
func (d *Deployer) AddAddon(ctx context.Context, project, kind string) (store.Addon, error) {
	k, ok := addonKinds[kind]
	if !ok {
		return store.Addon{}, fmt.Errorf("unknown add-on %q; available: %s", kind, strings.Join(AddonKindNames(), ", "))
	}
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return store.Addon{}, err
	}
	pw, _ := secret.Generate("hex:24") // hex: safe inside URLs and command lines
	a := store.Addon{ProjectID: p.ID, Kind: kind, Image: k.image, Password: pw}
	if err := d.store.AddAddon(ctx, a); err != nil {
		var existing []store.Addon
		if existing, _ = d.store.Addons(ctx, p.ID); slices.ContainsFunc(existing, func(e store.Addon) bool { return e.Kind == kind }) {
			return store.Addon{}, fmt.Errorf("%s already has a %s add-on", project, kind)
		}
		return store.Addon{}, err
	}
	if err := d.startAddon(ctx, p.Name, a, true); err != nil {
		// Keep the record: the reconciler retries, and the password must not change.
		return a, fmt.Errorf("%s was added but did not start yet: %w", kind, err)
	}
	return a, nil
}

// RemoveAddon detaches an add-on and deletes its container and data. Backups are kept.
func (d *Deployer) RemoveAddon(ctx context.Context, project, kind string) error {
	l := d.projectLock(project)
	l.Lock()
	defer l.Unlock()
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return err
	}
	if err := d.store.RemoveAddon(ctx, p.ID, kind); err != nil {
		return err
	}
	if err := d.rt.Remove(ctx, AddonContainer(project, kind), addonKinds[kind].stopGrace); err != nil {
		return err
	}
	return d.rt.RemoveVolume(ctx, VolumeName(project, kind))
}

// startAddon ensures an add-on's container runs and, with wait, that it is healthy.
func (d *Deployer) startAddon(ctx context.Context, project string, a store.Addon, wait bool) error {
	if err := d.rt.EnsureNetwork(ctx, ProjectNetwork(project)); err != nil {
		return err
	}
	if err := d.rt.EnsureVolume(ctx, VolumeName(project, a.Kind)); err != nil {
		return err
	}
	if err := d.rt.EnsureContainer(ctx, addonSpec(project, a)); err != nil || !wait {
		return err
	}
	return d.waitAddonHealthy(ctx, project, a.Kind)
}

func (d *Deployer) waitAddonHealthy(ctx context.Context, project, kind string) error {
	ctx, cancel := context.WithTimeout(ctx, d.opt.HealthTimeout)
	defer cancel()
	name := AddonContainer(project, kind)
	for {
		c, err := d.rt.Inspect(ctx, name)
		if err != nil {
			return err
		}
		if c.Health == "healthy" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s is %s after %s", kind, c.Health, d.opt.HealthTimeout)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// ensureAddons starts every add-on of p that is not running and, with wait, waits until all
// are healthy. Deploys wait; the reconciler does not.
func (d *Deployer) ensureAddons(ctx context.Context, p store.Project, wait bool) error {
	addons, err := d.store.Addons(ctx, p.ID)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range addons {
		if err := d.startAddon(ctx, p.Name, a, wait); err != nil {
			errs = append(errs, fmt.Errorf("add-on %s: %w", a.Kind, err))
		}
	}
	return errors.Join(errs...)
}

// AddonView is an add-on as shown to its owner, with what services receive.
type AddonView struct {
	Kind    string `json:"kind"`
	Image   string `json:"image"`
	EnvVar  string `json:"env_var"`
	URL     string `json:"url"`
	Status  string `json:"status"`
	Created string `json:"created_at"`
}

// Addons lists a project's add-ons with their live status.
func (d *Deployer) Addons(ctx context.Context, project string) ([]AddonView, error) {
	p, err := d.store.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	addons, err := d.store.Addons(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := []AddonView{}
	for _, a := range addons {
		status := "missing"
		if c, err := d.rt.Inspect(ctx, AddonContainer(project, a.Kind)); err == nil {
			status = c.Health
			if !c.Running {
				status = "stopped"
			}
		}
		out = append(out, AddonView{
			Kind: a.Kind, Image: a.Image, EnvVar: addonKinds[a.Kind].envVar,
			URL: addonFields(project, a)["url"], Status: status,
			Created: a.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}
