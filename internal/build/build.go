// Package build turns a source directory into a local image with docker buildx: from the
// app's Dockerfile when it names one, otherwise with Railpack's BuildKit frontend.
//
// Every command is printed before it runs, so a failed build can be reproduced by hand.
package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/deploy"
)

// Builder implements deploy.Builder.
type Builder struct {
	// Docker is the docker CLI, with the buildx plugin installed.
	Docker string
	// Railpack is the railpack CLI.
	Railpack string
	// Frontend is the Railpack BuildKit frontend image, pinned to the CLI's version.
	Frontend string
}

var _ deploy.Builder = (*Builder)(nil)

// Build builds spec.Image from spec.Dir.
func (b *Builder) Build(ctx context.Context, spec deploy.BuildSpec, w io.Writer) error {
	args := []string{"buildx", "build", "--load", "--progress=plain", "--tag", spec.Image}
	if spec.Dockerfile != "" {
		args = append(args, "--file", filepath.Join(spec.Dir, spec.Dockerfile))
		if spec.Target != "" {
			args = append(args, "--target", spec.Target)
		}
		return run(ctx, w, b.Docker, append(args, spec.Dir)...)
	}

	// The plan goes outside the source tree so it never ends up in the build context.
	tmp, err := os.MkdirTemp("", "kayly-plan-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	plan := filepath.Join(tmp, "railpack-plan.json")
	err = run(ctx, w, b.Railpack, "prepare", spec.Dir,
		"--plan-out", plan, "--info-out", filepath.Join(tmp, "railpack-info.json"))
	if err != nil {
		return err
	}
	args = append(args,
		"--build-arg", "BUILDKIT_SYNTAX="+b.Frontend,
		"--build-arg", "cache-key="+spec.CacheKey,
		"--file", plan,
		spec.Dir,
	)
	return run(ctx, w, b.Docker, args...)
}

// run prints and runs a command, streaming its combined output to w.
func run(ctx context.Context, w io.Writer, name string, args ...string) error {
	fmt.Fprintf(w, "$ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = w, w
	// On cancel, give the CLI a moment to stop BuildKit cleanly before it is killed.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(name), args[0], err)
	}
	return nil
}

// TrimCache removes the least recently used build cache until at most maxBytes remain. Cache
// is what makes a deploy fast, so it is kept up to that size; images of releases are not
// touched (Kayly prunes those itself).
func (b *Builder) TrimCache(ctx context.Context, maxBytes int64, w io.Writer) error {
	return run(ctx, w, b.Docker, "buildx", "prune", "--force", "--max-used-space", strconv.FormatInt(maxBytes, 10))
}
