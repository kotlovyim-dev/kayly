package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/api"
	"github.com/kotlovyim-dev/kayly/internal/build"
	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/docker"
	"github.com/kotlovyim-dev/kayly/internal/notify"
	"github.com/kotlovyim-dev/kayly/internal/offsite"
	"github.com/kotlovyim-dev/kayly/internal/proxy"
	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
	"github.com/kotlovyim-dev/kayly/web"
)

// Pinned infrastructure. Bump together with install.sh.
const (
	caddyImage       = "caddy:2.11.6@sha256:3422ce6de165df66534f9b9ba50efaf457114ec961763cc52f5dbdaac2972d73"
	railpackFrontend = "ghcr.io/railwayapp/railpack-frontend:v0.40.1@sha256:f1973377693af30c9b37a92c97c661c07b277ccdc6be909213c74c771f8d2d6d"
	proxyContainer   = "kayly-proxy"
)

// Paths on the host. The run directory's proxy/ subdirectory is the only one the proxy
// container sees: it holds Caddy's admin socket and the daemon's public socket. The admin
// socket of the daemon lives one level up, out of the container's reach.
type paths struct{ data, run string }

func (p paths) db() string          { return filepath.Join(p.data, "kayly.db") }
func (p paths) key() string         { return filepath.Join(p.data, "master.key") }
func (p paths) builds() string      { return filepath.Join(p.data, "builds") }
func (p paths) backups() string     { return filepath.Join(p.data, "backups") }
func (p paths) logs() string        { return filepath.Join(p.data, "logs") }
func (p paths) caddyConfig() string { return filepath.Join(p.data, "caddy.json") }
func (p paths) proxyDir() string    { return filepath.Join(p.run, "proxy") }
func (p paths) caddySock() string   { return filepath.Join(p.proxyDir(), "caddy.sock") }
func (p paths) publicSock() string  { return filepath.Join(p.proxyDir(), "kayly.sock") }

// adminSock is where the CLI on the server finds the daemon.
func adminSock(run string) string { return filepath.Join(run, "admin.sock") }

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	data := fs.String("data", "/var/lib/kayly", "state directory")
	run := fs.String("run", "/run/kayly", "socket directory")
	base := fs.String("base-domain", "", "every service gets <service>.<project>.<base-domain>, e.g. 203-0-113-7.sslip.io")
	domain := fs.String("domain", "", "hostname of the Kayly API (default kayly.<base-domain>)")
	email := fs.String("email", "", "contact email for Let's Encrypt (optional)")
	railpack := fs.String("railpack", "railpack", "path to the railpack CLI")
	cacheMax := fs.String("build-cache", "10g", "most disk the build cache may keep (k, m or g)")
	fs.Parse(args)
	if *domain == "" && *base != "" {
		*domain = "kayly." + *base
	}
	if *domain == "" {
		return errors.New("set --base-domain or --domain")
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	p := paths{data: *data, run: *run}
	// sun_path holds 108 bytes including the terminating NUL.
	for _, sock := range []string{p.caddySock(), p.publicSock(), adminSock(*run)} {
		if len(sock) > 107 {
			return fmt.Errorf("--run %s is too long: unix socket paths are limited to 107 bytes", *run)
		}
	}
	for _, dir := range []string{p.data, p.builds(), p.backups(), p.proxyDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	// The proxy runs as root without capabilities, so it obeys file modes and needs write
	// access to create its admin socket. The run directory stays 0700, which keeps every
	// other host user out of the open directory inside it.
	if err := os.Chmod(*run, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(p.proxyDir(), 0o777); err != nil {
		return err
	}

	key, err := secret.LoadOrCreateKey(p.key())
	if err != nil {
		return err
	}
	box, err := secret.NewBox(key)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, p.db(), box)
	if err != nil {
		return err
	}
	defer st.Close()
	rt, err := docker.New(ctx)
	if err != nil {
		return err
	}
	defer rt.Close()

	// Caddy creates the socket as root; |0222 lets the daemon connect even when it does not
	// run as root. Reaching the socket still requires passing the 0700 run directory.
	adminListen := "unix/" + p.caddySock() + "|0222"
	if err := ensureProxy(ctx, rt, p, adminListen); err != nil {
		return err
	}
	var cacheLimit config.ByteSize
	if err := cacheLimit.UnmarshalText([]byte(*cacheMax)); err != nil {
		return fmt.Errorf("--build-cache: %w", err)
	}
	builder := &build.Builder{Docker: "docker", Railpack: *railpack, Frontend: railpackFrontend}
	px := proxy.NewClient(p.caddySock())
	notifier := notify.New(st, log)
	uploader := offsite.New(st, p.backups(), notifier.Notify, log)
	d := deploy.New(st, rt,
		builder,
		px,
		deploy.Options{
			BaseDomain:  *base,
			Proxy:       proxy.Options{AdminListen: adminListen, Email: *email},
			ExtraRoutes: []proxy.Route{{Host: *domain, Upstream: "unix/" + p.publicSock()}},
			BackupDir:   p.backups(),
			LogDir:      p.logs(),
			DiskPath:    p.data,
			Notify:      notifier.Notify,
			KeyFile:     p.key(),
			Offsite:     uploader,
		}, log)

	if err := waitForSocket(ctx, p.caddySock(), time.Minute); err != nil {
		return fmt.Errorf("proxy did not start: %w", err)
	}
	// Before any listener opens: no deploy may start while interrupted ones are unmarked.
	if err := d.Recover(ctx); err != nil {
		return err
	}

	srv := api.New(st, d, p.builds(), log)
	srv.Version = version
	srv.Notifier = notifier
	srv.Offsite = uploader
	srv.UI = web.UI()
	if srv.UI == nil {
		log.Warn("this build has no web UI; build it with `npm run build` in web/")
	}
	public, err := listenUnix(p.publicSock(), 0o666)
	if err != nil {
		return err
	}
	admin, err := listenUnix(adminSock(*run), 0o600)
	if err != nil {
		return err
	}
	servers := []*http.Server{
		{Handler: srv.Handler(false), ReadHeaderTimeout: 10 * time.Second},
		{Handler: srv.Handler(true), ReadHeaderTimeout: 10 * time.Second},
	}
	errc := make(chan error, 2)
	go func() { errc <- servers[0].Serve(public) }()
	go func() { errc <- servers[1].Serve(admin) }()

	// Reconcile now (cleaning up after a crash may wait on container grace periods, so the
	// API is already serving), then every 30 s.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			if err := d.Reconcile(ctx); err != nil {
				log.Error("reconcile", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if err := ensureProxy(ctx, rt, p, adminListen); err != nil {
				log.Error("ensure proxy", "err", err)
			}
			d.DailyBackups(ctx)
			d.Watch(ctx)
		}
	}()
	// Backups reach the bucket within minutes of being written; an outage only delays them.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			if err := uploader.Sync(ctx); err != nil {
				log.Error("off-site sync", "err", err)
			}
			d.DrillIfDue(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	// The build cache grows with every deploy; keep the recent part of it.
	go func() {
		t := time.NewTimer(10 * time.Minute) // not while the server is still starting up
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if err := builder.TrimCache(ctx, int64(cacheLimit), io.Discard); err != nil {
				log.Error("trim build cache", "err", err)
			}
			t.Reset(24 * time.Hour)
		}
	}()
	log.Info("kayly serving", "api", "https://"+*domain, "admin_socket", adminSock(*run))

	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	log.Info("shutting down; waiting for running deploys")
	// Deploys run to completion; systemd's TimeoutStopSec must be longer than this.
	shutdown, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	for _, s := range servers {
		s.Shutdown(shutdown)
	}
	return nil
}

// ensureProxy keeps the Caddy container running. Its first config only moves the admin
// API onto the shared socket; after that Caddy resumes the last config the daemon loaded,
// so it keeps routing while the daemon is down.
func ensureProxy(ctx context.Context, rt *docker.Runtime, p paths, adminListen string) error {
	initial, _ := json.Marshal(map[string]any{"admin": map[string]string{"listen": adminListen}})
	if err := os.WriteFile(p.caddyConfig(), initial, 0o644); err != nil {
		return err
	}
	if err := rt.EnsureNetwork(ctx, deploy.EdgeNetwork); err != nil {
		return err
	}
	for _, v := range []string{"kayly_proxy_data", "kayly_proxy_config"} {
		if err := rt.EnsureVolume(ctx, v); err != nil {
			return err
		}
	}
	return rt.EnsureContainer(ctx, deploy.Spec{
		Name:     proxyContainer,
		Image:    caddyImage,
		Cmd:      []string{"caddy", "run", "--config", "/etc/kayly/caddy.json", "--resume"},
		Networks: []deploy.Network{{Name: deploy.EdgeNetwork}},
		Mounts: []deploy.Mount{
			{Volume: "kayly_proxy_data", Path: "/data"},
			{Volume: "kayly_proxy_config", Path: "/config"},
			{Bind: p.caddyConfig(), Path: "/etc/kayly/caddy.json", ReadOnly: true},
			{Bind: p.proxyDir(), Path: p.proxyDir()},
		},
		Ports: []deploy.Port{
			{Host: 80, Container: 80, Proto: "tcp"},
			{Host: 443, Container: 443, Proto: "tcp"},
			{Host: 443, Container: 443, Proto: "udp"},
		},
		CapAdd:  []string{"NET_BIND_SERVICE"},
		Restart: true,
	})
}

func listenUnix(path string, mode os.FileMode) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func waitForSocket(ctx context.Context, path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		var d net.Dialer
		if c, err := d.DialContext(ctx, "unix", path); err == nil {
			return c.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
