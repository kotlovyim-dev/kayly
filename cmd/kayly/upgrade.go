package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/api"
	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/upgrade"
)

// upgradeServer is `kayly upgrade [version]`: on the server, it fetches a signed release, takes
// a Kayly backup, swaps the binary and restarts the daemon. Apps and the proxy are separate
// containers and keep serving throughout (ARCHITECTURE.md §2, invariant 4).
func upgradeServer(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	local := fs.String("binary", "", "install this kayly binary instead of downloading a release (not signature-checked)")
	wait := fs.Duration("wait", 10*time.Minute, "how long to wait for the restarted daemon; it first lets running deploys finish")
	fs.Parse(args)
	if fs.NArg() > 1 {
		return errors.New("usage: kayly upgrade [version] [-binary path]")
	}
	ctx := context.Background()
	c, err := newClient()
	if err != nil {
		return err
	}
	if c.token != "" || c.base != "http://kayly" {
		return errors.New("run kayly upgrade on the server, as root (sudo kayly upgrade)")
	}
	var before api.ServerView
	if err := c.do(ctx, "GET", "/api/v1/server", nil, &before); err != nil {
		return fmt.Errorf("ask the running daemon its version: %w", err)
	}

	bin, target, err := pickRelease(ctx, *local, fs.Arg(0), before.Version)
	if err != nil || bin == nil {
		return err
	}

	fmt.Println("taking a backup of Kayly's database and key…")
	var b deploy.Backup
	if err := c.do(ctx, "POST", "/api/v1/backups/kayly", nil, &b); err != nil {
		return fmt.Errorf("back up Kayly before upgrading: %w", err)
	}
	fmt.Printf("  %s (%d KB)\n", b.Name, b.Size/1024)

	exe, err := daemonBinary(ctx)
	if err != nil {
		return err
	}
	if err := upgrade.Install(bin, exe); err != nil {
		return err
	}
	fmt.Printf("installed %s (the previous binary is %s.previous)\nrestarting the daemon; running deploys finish first…\n", exe, exe)
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", "kayly").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart kayly: %w: %s\n  see journalctl -u kayly; to go back: mv %s.previous %s && systemctl restart kayly", err, out, exe, exe)
	}

	after, err := waitForDaemon(ctx, c, *wait, target)
	if err != nil {
		return fmt.Errorf("%w\n  see journalctl -u kayly; to go back: mv %s.previous %s && systemctl restart kayly, then restore %s if the database changed", err, exe, exe, b.Name)
	}
	fmt.Printf("kayly %s is running; apps and the proxy kept serving.\n", after)
	return nil
}

// daemonBinary is the file the kayly unit runs, which is what an upgrade replaces, whichever
// copy of the CLI asks for it.
func daemonBinary(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "show", "--property", "MainPID", "--value", "kayly").Output()
	pid := strings.TrimSpace(string(out))
	if err != nil || pid == "" || pid == "0" {
		return "", fmt.Errorf("the kayly unit is not running under systemd (%v); start it first: systemctl start kayly", err)
	}
	exe, err := os.Readlink(filepath.Join("/proc", pid, "exe"))
	if err != nil {
		return "", fmt.Errorf("find the daemon's binary: %w", err)
	}
	return strings.TrimSuffix(exe, " (deleted)"), nil
}

// pickRelease returns the binary to install and its version. A nil binary with no error means
// there is nothing to do.
func pickRelease(ctx context.Context, local, want, running string) ([]byte, string, error) {
	if local != "" {
		bin, err := os.ReadFile(local)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", local, err)
		}
		fmt.Printf("installing %s over kayly %s (a local file: its signature is not checked)\n", local, running)
		return bin, "", nil
	}
	src, err := upgrade.GitHub()
	if err != nil {
		return nil, "", err
	}
	if want == "" {
		if want, err = src.LatestVersion(ctx); err != nil {
			return nil, "", err
		}
	}
	if err := upgrade.Valid(want); err != nil {
		return nil, "", err
	}
	// A daemon built from source reports "dev", which compares with nothing: it may always move
	// to a release.
	if cmp, err := upgrade.Compare(want, running); err == nil && cmp == 0 {
		fmt.Printf("kayly %s is already running\n", running)
		return nil, "", nil
	} else if err == nil && cmp < 0 {
		return nil, "", fmt.Errorf("%s is older than the running %s; Kayly's database only migrates forward. "+
			"To go back, restore the Kayly backup taken before the upgrade (see ARCHITECTURE.md §8)", want, running)
	}
	fmt.Printf("downloading kayly %s for linux/%s…\n", want, runtime.GOARCH)
	bin, err := src.Fetch(ctx, want, runtime.GOARCH)
	if err != nil {
		return nil, "", err
	}
	fmt.Println("  signature and checksum verified")
	return bin, want, nil
}

// waitForDaemon polls the admin socket until the restarted daemon answers, and returns its
// version. `systemctl restart` returns only once the old daemon has stopped, so any answer comes
// from the new one; want, when set, is the version it must report.
func waitForDaemon(ctx context.Context, c *client, timeout time.Duration, want string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		var sv api.ServerView
		if err := c.do(ctx, "GET", "/api/v1/server", nil, &sv); err == nil {
			if want != "" && sv.Version != want {
				return "", fmt.Errorf("the daemon came back as %s, not %s", sv.Version, want)
			}
			return sv.Version, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("the daemon did not come back within %s", timeout)
		case <-time.After(time.Second):
		}
	}
}
