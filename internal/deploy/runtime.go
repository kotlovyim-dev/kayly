package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Runtime is what the pipeline needs from the container engine. internal/runtime
// implements it with Docker; tests use an in-memory fake.
type Runtime interface {
	EnsureNetwork(ctx context.Context, name string) error
	EnsureVolume(ctx context.Context, name string) error
	// Run creates and starts a container. It fails if the name is taken.
	Run(ctx context.Context, spec Spec) error
	// Wait blocks until the container exits and returns its exit code.
	Wait(ctx context.Context, name string) (int, error)
	// Logs writes the container's output to w. With follow it returns once the container
	// stops; tail > 0 limits the output to the last lines.
	Logs(ctx context.Context, name string, follow bool, tail int, w io.Writer) error
	// Inspect returns ErrNoContainer if the container does not exist.
	Inspect(ctx context.Context, name string) (Container, error)
	// Remove stops the container (SIGTERM, then SIGKILL after grace) and removes it.
	// A missing container is not an error.
	Remove(ctx context.Context, name string, grace time.Duration) error
	// List returns every container carrying the LabelProject label.
	List(ctx context.Context) ([]Container, error)
	ImageExists(ctx context.Context, ref string) (bool, error)
	RemoveImage(ctx context.Context, ref string) error
}

// ErrNoContainer is returned by Runtime.Inspect for a missing container.
var ErrNoContainer = errors.New("no such container")

// Builder turns a source directory into a tagged local image.
type Builder interface {
	Build(ctx context.Context, spec BuildSpec, log io.Writer) error
}

// BuildSpec describes one image build.
type BuildSpec struct {
	Dir string
	// Dockerfile is relative to Dir. Empty means Railpack.
	Dockerfile string
	Target     string
	Image      string
	// CacheKey separates build caches of different projects.
	CacheKey string
}

// Proxy loads a full proxy config. Implemented by proxy.Client.
type Proxy interface {
	Load(ctx context.Context, cfg []byte) error
}

// Spec describes a container to run.
type Spec struct {
	Name  string
	Image string
	// Cmd overrides the image's command. Nil keeps the image default.
	Cmd      []string
	Env      map[string]string
	Labels   map[string]string
	Networks []Network
	Mounts   []Mount
	ReadOnly bool
	// Restart keeps the container running (restart unless-stopped). Off for one-off runs.
	Restart bool
	// CapAdd adds capabilities back after all are dropped.
	CapAdd []string
}

// Network attaches a container to a network under extra DNS names.
type Network struct {
	Name    string
	Aliases []string
}

// Mount is a named volume mounted at Path.
type Mount struct {
	Volume string
	Path   string
}

// Container is the observed state of a container.
type Container struct {
	Name    string
	Labels  map[string]string
	Running bool
	// Restarting is true while Docker waits to restart a crashed container.
	Restarting   bool
	ExitCode     int
	RestartCount int
	StartedAt    time.Time
	// IP is the container's address on the edge network, empty if not attached.
	IP string
}

// Labels Kayly puts on every container it creates, so actual state maps back to desired state.
const (
	LabelProject = "dev.kayly.project"
	LabelRelease = "dev.kayly.release"
	LabelRole    = "dev.kayly.role"
	LabelService = "dev.kayly.service"

	RoleService = "service"
	RoleRelease = "release"
)

// EdgeNetwork is shared by the proxy and every service with a port.
const EdgeNetwork = "kayly"

// Names use "_" as separator: project, service and volume names cannot contain it, so two
// different combinations never produce the same name.

// ProjectNetwork is the private network of a project's services, release step and add-ons.
func ProjectNetwork(project string) string { return "kayly_" + project }

// VolumeName is the Docker volume backing a project volume.
func VolumeName(project, volume string) string { return "kayly_" + project + "_" + volume }

// ContainerName is the container of a service (or the release step) of a release.
func ContainerName(project, service string, seq int) string {
	return fmt.Sprintf("%s_%s_r%d", project, service, seq)
}

// ImageName is the local image of a release.
func ImageName(project string, seq int) string { return fmt.Sprintf("kayly/%s:r%d", project, seq) }

func labels(project string, seq int, role, service string) map[string]string {
	l := map[string]string{LabelProject: project, LabelRelease: strconv.Itoa(seq), LabelRole: role}
	if service != "" {
		l[LabelService] = service
	}
	return l
}
