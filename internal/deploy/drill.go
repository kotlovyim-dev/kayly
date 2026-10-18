package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

const (
	drillSetting  = "drill_last"
	drillInterval = 7 * 24 * time.Hour
)

// DrillIfDue runs Drill when the last one is a week old or more. The time is recorded even
// when the drill fails, so a broken bucket is reported weekly, not every poll.
func (d *Deployer) DrillIfDue(ctx context.Context) {
	if d.opt.Offsite == nil {
		return
	}
	if on, err := d.opt.Offsite.Enabled(ctx); err != nil || !on {
		return
	}
	last, _ := d.store.Setting(ctx, drillSetting)
	if t, err := time.Parse(time.RFC3339, last); err == nil && time.Since(t) < drillInterval {
		return
	}
	if err := d.store.SetSetting(ctx, drillSetting, time.Now().UTC().Format(time.RFC3339)); err != nil {
		d.log.Error("record drill", "err", err)
		return
	}
	if err := d.Drill(ctx); err != nil {
		d.log.Error("restore drill", "err", err)
	}
}

// Drill proves the off-site backups restore: for every project with a Postgres add-on it
// downloads the newest dump from the bucket, restores it into a throwaway container of the
// add-on's image and checks that pg_restore accepts it. A failure sends a notification, as a
// failed backup does.
func (d *Deployer) Drill(ctx context.Context) error {
	if d.opt.Offsite == nil {
		return errors.New("off-site backups are not set up")
	}
	projects, err := d.store.Projects(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range projects {
		addons, err := d.store.Addons(ctx, p.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, a := range addons {
			if a.Kind != "postgres" {
				continue
			}
			tables, name, err := d.drillPostgres(ctx, p, a)
			switch {
			case errors.Is(err, errNothingToDrill):
				d.log.Info("restore drill: no dump off-site yet", "project", p.Name)
			case err != nil:
				d.alert(ctx, "✗ %s: restore drill failed: %v", p.Name, err)
				errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			default:
				d.log.Info("restore drill passed", "project", p.Name, "backup", name, "tables", tables)
			}
		}
	}
	return errors.Join(errs...)
}

var errNothingToDrill = errors.New("no off-site dump")

func (d *Deployer) drillPostgres(ctx context.Context, p store.Project, a store.Addon) (tables, name string, err error) {
	remote, err := d.opt.Offsite.List(ctx, p.Name)
	if err != nil {
		return "", "", err
	}
	for _, b := range remote {
		if strings.HasSuffix(b.Name, ".dump") {
			name = b.Name
			break
		}
	}
	if name == "" {
		return "", "", errNothingToDrill
	}
	path, cleanup, err := d.fetch(ctx, p, name, RestoreSource{Project: p.Name, Remote: true})
	if err != nil {
		return "", name, err
	}
	defer cleanup()

	pw, err := secret.Generate("hex:16")
	if err != nil {
		return "", name, err
	}
	// Same image and settings as the real add-on, but no network and a volume of its own.
	spec := AddonSpec(p.Name, store.Addon{ProjectID: p.ID, Kind: "postgres", Image: a.Image, Password: pw})
	cname, vol := "kayly_drill_"+p.Name, "kayly_drill_"+p.Name
	spec.Name, spec.Networks, spec.Labels = cname, nil, labels(p.Name, 0, RoleBackup, "drill")
	spec.Mounts = []Mount{{Volume: vol, Path: addonKinds["postgres"].dataPath}}

	cctx := context.WithoutCancel(ctx)
	defer func() {
		d.rt.Remove(cctx, cname, 10*time.Second)
		d.rt.RemoveVolume(cctx, vol)
	}()
	if err := d.rt.Remove(cctx, cname, 0); err != nil { // left over from a crash
		return "", name, err
	}
	if err := d.rt.RemoveVolume(cctx, vol); err != nil {
		return "", name, err
	}
	if err := d.rt.EnsureVolume(ctx, vol); err != nil {
		return "", name, err
	}
	if err := d.rt.EnsureContainer(ctx, spec); err != nil {
		return "", name, err
	}
	if err := d.waitContainerHealthy(ctx, cname); err != nil {
		return "", name, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", name, err
	}
	defer f.Close()
	if _, err := d.rt.Exec(ctx, cname,
		[]string{"pg_restore", "-U", "postgres", "--no-owner", "--single-transaction", "-d", pgDatabase(p.Name)}, f, io.Discard); err != nil {
		return "", name, fmt.Errorf("restore %s: %w", name, err)
	}
	var out strings.Builder
	if _, err := d.rt.Exec(ctx, cname, []string{"psql", "-U", "postgres", "-d", pgDatabase(p.Name), "-tAc",
		"select count(*) from information_schema.tables where table_schema not in ('pg_catalog','information_schema')"}, nil, &out); err != nil {
		return "", name, fmt.Errorf("check %s: %w", name, err)
	}
	return strings.TrimSpace(out.String()), name, nil
}
