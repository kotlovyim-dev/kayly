package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/config"
)

// waitHealthy waits until every service of a release is healthy, or fails with the first
// service that crashed or did not become healthy in time.
func (d *Deployer) waitHealthy(ctx context.Context, project string, seq int, cfg *config.Config, w io.Writer) error {
	ctx, cancel := context.WithTimeoutCause(ctx, d.opt.HealthTimeout, errHealthTimeout)
	defer cancel()
	ctx, fail := context.WithCancelCause(ctx)
	defer fail(nil)

	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		wg.Go(func() {
			if err := d.waitService(ctx, ContainerName(project, name, seq), name, cfg.Services[name], w); err != nil {
				// The first failure stops the others; theirs would only echo it.
				once.Do(func() { first = err; fail(err) })
			}
		})
	}
	wg.Wait()
	return first
}

var errHealthTimeout = errors.New("health check timed out")

func (d *Deployer) waitService(ctx context.Context, container, name string, svc config.Service, w io.Writer) error {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var last error
	for {
		c, err := d.rt.Inspect(ctx, container)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if !c.Running || c.RestartCount > 0 {
			fmt.Fprintf(w, "%s crashed (exit code %d), last lines:\n", name, c.ExitCode)
			d.rt.Logs(context.WithoutCancel(ctx), container, false, 30, w)
			return fmt.Errorf("%s crashed on start (exit code %d)", name, c.ExitCode)
		}
		switch {
		case svc.Port == 0:
			if time.Since(c.StartedAt) >= d.settle {
				fmt.Fprintf(w, "%s running\n", name)
				return nil
			}
		case c.IP == "":
			last = errors.New("no address on the edge network yet")
		default:
			if last = d.checkHealth(ctx, net.JoinHostPort(c.IP, strconv.Itoa(svc.Port)), svc.Health); last == nil {
				fmt.Fprintf(w, "%s healthy\n", name)
				return nil
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), errHealthTimeout) {
				return fmt.Errorf("%s not healthy after %s: %v", name, d.opt.HealthTimeout, last)
			}
			return context.Cause(ctx)
		case <-tick.C:
		}
	}
}

var healthClient = &http.Client{
	Timeout: 5 * time.Second,
	// A redirect from a health endpoint is not health.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// probe checks addr: an HTTP GET of path must answer 2xx; without a path, a TCP connect
// must succeed.
func probe(ctx context.Context, addr, path string) error {
	if path == "" {
		var dl net.Dialer
		c, err := dl.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return c.Close()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return err
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return nil
}
