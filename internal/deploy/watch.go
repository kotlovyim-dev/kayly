package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// What counts as trouble (ARCHITECTURE.md §9). The owner is told once when a service enters
// it and once when it leaves, never on every poll.
const (
	loopRestarts = 3                // restarts within loopWindow make a restart loop
	loopWindow   = 10 * time.Minute // and a service is stable again after a window without any
	probeFails   = 3                // consecutive failed health probes of a live service
	diskAlert    = 85               // percent used
	diskClear    = 80               // percent used at which the alert ends
)

// watcher remembers what the owner has already been told, per live container and for the disk.
type watcher struct {
	mu   sync.Mutex
	svc  map[string]*svcState
	disk bool // the owner knows the disk is nearly full
}

type svcState struct {
	seen     bool
	restarts int         // RestartCount at the last look
	events   []time.Time // when restarts were noticed, within loopWindow
	looping  bool        // the owner knows about the loop
	failures int         // consecutive failed probes
	sick     bool        // the owner knows the probes fail
}

// Watch looks at every live service and at the disk and sends a notification when something
// enters or leaves trouble: a restart loop, a failing health check, a disk above 85 %. It is
// meant to run on every reconcile tick and never changes anything.
func (d *Deployer) Watch(ctx context.Context) {
	d.watch.mu.Lock()
	defer d.watch.mu.Unlock()
	if d.watch.svc == nil {
		d.watch.svc = map[string]*svcState{}
	}
	d.watchServices(ctx)
	d.watchDisk(ctx)
}

func (d *Deployer) watchServices(ctx context.Context) {
	projects, err := d.store.Projects(ctx)
	if err != nil {
		d.log.Error("watch", "err", err)
		return
	}
	live := map[string]bool{}
	for _, p := range projects {
		if p.ActiveRelease == 0 {
			continue
		}
		// A deploy, rollback or restore moves containers around on purpose.
		l := d.projectLock(p.Name)
		if !l.TryLock() {
			for name := range d.watch.svc {
				if strings.HasPrefix(name, p.Name+"_") {
					live[name] = true // keep what we know until the project is quiet again
				}
			}
			continue
		}
		l.Unlock()
		rel, err := d.store.ReleaseByID(ctx, p.ActiveRelease)
		if err != nil {
			continue
		}
		cfg, err := config.Parse(rel.Config)
		if err != nil {
			continue
		}
		for service, svc := range cfg.Services {
			name := ContainerName(p.Name, service, rel.Seq)
			live[name] = true
			// Inspect, not List: only it reports the restart count and the address.
			c, err := d.rt.Inspect(ctx, name)
			if err != nil {
				if !errors.Is(err, ErrNoContainer) { // else the reconciler is about to recreate it
					d.log.Error("watch", "container", name, "err", err)
				}
				continue
			}
			d.watchService(ctx, p, service, svc, c)
		}
	}
	for name := range d.watch.svc { // forget what is gone, e.g. the previous release
		if !live[name] {
			delete(d.watch.svc, name)
		}
	}
}

func (d *Deployer) watchService(ctx context.Context, p store.Project, service string, svc config.Service, c Container) {
	st := d.watch.svc[c.Name]
	if st == nil {
		st = &svcState{}
		d.watch.svc[c.Name] = st
	}
	now := d.clock()

	// Restart loop. The first look only sets the baseline: restarts from before the daemon
	// started are history.
	if st.seen && c.RestartCount > st.restarts {
		for range c.RestartCount - st.restarts {
			st.events = append(st.events, now)
		}
	}
	st.seen, st.restarts = true, c.RestartCount
	for len(st.events) > 0 && now.Sub(st.events[0]) > loopWindow {
		st.events = st.events[1:]
	}
	switch {
	case len(st.events) >= loopRestarts && !st.looping:
		st.looping = true
		d.alert(ctx, "✗ %s: %s keeps restarting (%d times in %d minutes)\n%s",
			p.Name, service, len(st.events), int(loopWindow.Minutes()), d.lastLines(ctx, c.Name))
	case len(st.events) == 0 && st.looping && c.Running && !c.Restarting:
		st.looping = false
		d.alert(ctx, "✓ %s: %s is stable again", p.Name, service)
	}

	// Health of a service that takes traffic.
	if svc.Port == 0 || c.IP == "" || !c.Running {
		return
	}
	if err := d.checkHealth(ctx, net.JoinHostPort(c.IP, strconv.Itoa(svc.Port)), svc.Health); err != nil {
		st.failures++
		if st.failures >= probeFails && !st.sick {
			st.sick = true
			d.alert(ctx, "✗ %s: %s is running but fails its health check: %v\n%s",
				p.Name, service, err, d.lastLines(ctx, c.Name))
		}
		return
	}
	st.failures = 0
	if st.sick {
		st.sick = false
		d.alert(ctx, "✓ %s: %s passes its health check again", p.Name, service)
	}
}

// lastLines is the tail of a container's log for a notification, short enough to read in a
// chat message.
func (d *Deployer) lastLines(ctx context.Context, container string) string {
	var out strings.Builder
	d.rt.Logs(context.WithoutCancel(ctx), container, false, 5, &out)
	s := strings.TrimSpace(out.String())
	if r := []rune(s); len(r) > 600 {
		s = "…" + string(r[len(r)-600:])
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

func (d *Deployer) watchDisk(ctx context.Context) {
	if d.opt.DiskPath == "" {
		return
	}
	used, total, err := d.diskUsage(d.opt.DiskPath)
	if err != nil || total == 0 {
		d.log.Error("watch disk", "err", err)
		return
	}
	pct := int(used * 100 / total)
	switch {
	case pct >= diskAlert && !d.watch.disk:
		d.watch.disk = true
		d.alert(ctx, "✗ the server's disk is %d%% full (%s of %s). Old images, build cache and backups are the usual cause.",
			pct, HumanBytes(used), HumanBytes(total))
	case pct < diskClear && d.watch.disk:
		d.watch.disk = false
		d.alert(ctx, "✓ the server's disk is down to %d%%", pct)
	}
}

// HumanBytes writes a size the way people read it: 1.5 GiB.
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
