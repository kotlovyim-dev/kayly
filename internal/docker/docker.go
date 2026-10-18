// Package docker implements deploy.Runtime with the Docker Engine API.
package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/kotlovyim-dev/kayly/internal/deploy"
)

// Runtime is a deploy.Runtime backed by a Docker daemon.
type Runtime struct {
	c *client.Client
}

var _ deploy.Runtime = (*Runtime)(nil)

// New connects to the Docker daemon configured by the environment (DOCKER_HOST, or the
// default socket) and checks that it answers.
func New(ctx context.Context) (*Runtime, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	if _, err := c.Ping(ctx, client.PingOptions{}); err != nil {
		c.Close()
		return nil, fmt.Errorf("docker: daemon not reachable: %w", err)
	}
	return &Runtime{c: c}, nil
}

// Close releases the client's connections.
func (r *Runtime) Close() error { return r.c.Close() }

// EnsureNetwork creates a bridge network unless it exists.
func (r *Runtime) EnsureNetwork(ctx context.Context, name string) error {
	if _, err := r.c.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: inspect network %s: %w", name, err)
	}
	_, err := r.c.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge"})
	if err != nil && !cerrdefs.IsConflict(err) { // created concurrently
		return fmt.Errorf("docker: create network %s: %w", name, err)
	}
	return nil
}

// EnsureVolume creates a local volume unless it exists. Creating an existing volume is a
// no-op in Docker.
func (r *Runtime) EnsureVolume(ctx context.Context, name string) error {
	if _, err := r.c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name}); err != nil {
		return fmt.Errorf("docker: create volume %s: %w", name, err)
	}
	return nil
}

// Run creates and starts a container with Kayly's hardening defaults: all capabilities
// dropped (CapAdd excepted), no privilege escalation, rotated logs.
func (r *Runtime) Run(ctx context.Context, s deploy.Spec) error {
	env := make([]string, 0, len(s.Env))
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, k+"="+s.Env[k])
	}
	restart := container.RestartPolicy{Name: container.RestartPolicyDisabled}
	if s.Restart {
		restart.Name = container.RestartPolicyUnlessStopped
	}
	host := &container.HostConfig{
		RestartPolicy:  restart,
		CapDrop:        []string{"ALL"},
		CapAdd:         s.CapAdd,
		SecurityOpt:    []string{"no-new-privileges:true"},
		ReadonlyRootfs: s.ReadOnly,
		Init:           &s.Init,
		LogConfig: container.LogConfig{
			Type:   "json-file",
			Config: map[string]string{"max-size": "10m", "max-file": "5"},
		},
	}
	if s.MemoryBytes > 0 {
		host.Memory, host.MemorySwap = s.MemoryBytes, s.MemoryBytes // equal: no swap
	}
	if s.CPUs > 0 {
		host.NanoCPUs = int64(s.CPUs * 1e9)
	}
	if s.ReadOnly {
		host.Tmpfs = map[string]string{"/tmp": ""}
	}
	for _, m := range s.Mounts {
		if m.Bind != "" {
			host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeBind, Source: m.Bind, Target: m.Path, ReadOnly: m.ReadOnly})
		} else {
			host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: m.Volume, Target: m.Path, ReadOnly: m.ReadOnly})
		}
	}
	var exposed network.PortSet
	if len(s.Ports) > 0 {
		exposed, host.PortBindings = network.PortSet{}, network.PortMap{}
	}
	for _, p := range s.Ports {
		port, err := network.ParsePort(fmt.Sprintf("%d/%s", p.Container, p.Proto))
		if err != nil {
			return fmt.Errorf("docker: %s: %w", s.Name, err)
		}
		exposed[port] = struct{}{}
		host.PortBindings[port] = append(host.PortBindings[port], network.PortBinding{HostPort: strconv.Itoa(p.Host)})
	}
	netcfg := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
	for _, n := range s.Networks {
		netcfg.EndpointsConfig[n.Name] = &network.EndpointSettings{Aliases: n.Aliases}
	}
	if len(s.Networks) > 0 {
		host.NetworkMode = container.NetworkMode(s.Networks[0].Name)
	}

	var entrypoint []string
	if s.Cmd != nil && !s.KeepEntrypoint {
		// [""] tells the engine to drop the image's entrypoint (as `docker run --entrypoint ""`).
		// Without it, an image with ENTRYPOINT ["bash", "-c"], as Railpack builds, would run
		// only the first word of the command.
		entrypoint = []string{""}
	}
	res, err := r.c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: s.Name,
		Config: &container.Config{
			Image:        s.Image,
			Entrypoint:   entrypoint,
			Cmd:          s.Cmd,
			Env:          env,
			Labels:       s.Labels,
			ExposedPorts: exposed,
			Healthcheck:  healthcheck(s.Health),
		},
		HostConfig:       host,
		NetworkingConfig: netcfg,
	})
	if err != nil {
		return fmt.Errorf("docker: create %s: %w", s.Name, err)
	}
	if _, err := r.c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		// Do not leave a created-but-stopped container holding the name.
		r.c.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true})
		return fmt.Errorf("docker: start %s: %w", s.Name, err)
	}
	return nil
}

// Wait blocks until the container stops and returns its exit code.
func (r *Runtime) Wait(ctx context.Context, name string) (int, error) {
	res := r.c.ContainerWait(ctx, name, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case w := <-res.Result:
		if w.Error != nil {
			return 0, fmt.Errorf("docker: wait %s: %s", name, w.Error.Message)
		}
		return int(w.StatusCode), nil
	case err := <-res.Error:
		return 0, fmt.Errorf("docker: wait %s: %w", name, err)
	}
}

// Logs copies the container's stdout and stderr to w.
func (r *Runtime) Logs(ctx context.Context, name string, follow bool, tail int, w io.Writer) error {
	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: follow, Tail: "all"}
	if tail > 0 {
		opts.Tail = strconv.Itoa(tail)
	}
	rc, err := r.c.ContainerLogs(ctx, name, opts)
	if err != nil {
		return fmt.Errorf("docker: logs %s: %w", name, err)
	}
	defer rc.Close()
	// Containers run without a TTY, so the stream is multiplexed.
	if _, err := stdcopy.StdCopy(w, w, rc); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("docker: logs %s: %w", name, err)
	}
	return nil
}

// Inspect returns the state of a container, or deploy.ErrNoContainer.
func (r *Runtime) Inspect(ctx context.Context, name string) (deploy.Container, error) {
	res, err := r.c.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return deploy.Container{}, fmt.Errorf("%s: %w", name, deploy.ErrNoContainer)
	}
	if err != nil {
		return deploy.Container{}, fmt.Errorf("docker: inspect %s: %w", name, err)
	}
	ci := res.Container
	c := deploy.Container{Name: name, RestartCount: ci.RestartCount}
	if ci.Config != nil {
		c.Labels = ci.Config.Labels
	}
	if st := ci.State; st != nil {
		c.Running, c.Restarting, c.ExitCode = st.Running, st.Restarting, st.ExitCode
		c.StartedAt, _ = time.Parse(time.RFC3339Nano, st.StartedAt)
		if st.Health != nil {
			c.Health = string(st.Health.Status)
		}
	}
	if ci.NetworkSettings != nil {
		if ep := ci.NetworkSettings.Networks[deploy.EdgeNetwork]; ep != nil && ep.IPAddress.IsValid() {
			c.IP = ep.IPAddress.String()
		}
	}
	return c, nil
}

// Remove stops a container with the given grace period, then removes it.
func (r *Runtime) Remove(ctx context.Context, name string, grace time.Duration) error {
	secs := int(grace.Seconds())
	_, err := r.c.ContainerStop(ctx, name, client.ContainerStopOptions{Timeout: &secs})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: stop %s: %w", name, err)
	}
	_, err = r.c.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	if err != nil && !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) { // conflict: removal already in progress
		return fmt.Errorf("docker: remove %s: %w", name, err)
	}
	return nil
}

// List returns every container (running or not) labelled by Kayly.
func (r *Runtime) List(ctx context.Context) ([]deploy.Container, error) {
	res, err := r.c.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", deploy.LabelProject),
	})
	if err != nil {
		return nil, fmt.Errorf("docker: list containers: %w", err)
	}
	out := make([]deploy.Container, 0, len(res.Items))
	for _, s := range res.Items {
		name := ""
		if len(s.Names) > 0 {
			name = s.Names[0][1:] // the API prefixes names with "/"
		}
		out = append(out, deploy.Container{
			Name:       name,
			Labels:     s.Labels,
			Running:    s.State == container.StateRunning,
			Restarting: s.State == container.StateRestarting,
		})
	}
	return out, nil
}

// ImageExists reports whether ref is present locally.
func (r *Runtime) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, err := r.c.ImageInspect(ctx, ref)
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("docker: inspect image %s: %w", ref, err)
	}
	return true, nil
}

// RemoveImage deletes a local image. A missing image is not an error.
func (r *Runtime) RemoveImage(ctx context.Context, ref string) error {
	_, err := r.c.ImageRemove(ctx, ref, client.ImageRemoveOptions{})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: remove image %s: %w", ref, err)
	}
	return nil
}

// LabelSpec holds a hash of the spec a long-lived container was created from.
const LabelSpec = "dev.kayly.spec"

// EnsureContainer keeps a long-lived infrastructure container (the proxy, add-ons) running
// as spec describes. A container that runs the same spec is left alone; one that is
// stopped or built from a different spec is replaced. The image is pulled if missing.
func (r *Runtime) EnsureContainer(ctx context.Context, spec deploy.Spec) error {
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:8])
	c, err := r.Inspect(ctx, spec.Name)
	switch {
	case err == nil && c.Labels[LabelSpec] == hash && (c.Running || c.Restarting):
		return nil
	case err != nil && !errors.Is(err, deploy.ErrNoContainer):
		return err
	}
	if ok, err := r.ImageExists(ctx, spec.Image); err != nil {
		return err
	} else if !ok {
		if err := r.PullImage(ctx, spec.Image); err != nil {
			return err
		}
	}
	if err := r.Remove(ctx, spec.Name, 30*time.Second); err != nil {
		return err
	}
	spec.Labels = maps.Clone(spec.Labels)
	if spec.Labels == nil {
		spec.Labels = map[string]string{}
	}
	spec.Labels[LabelSpec] = hash
	return r.Run(ctx, spec)
}

// PullImage downloads an image.
func (r *Runtime) PullImage(ctx context.Context, ref string) error {
	res, err := r.c.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("docker: pull %s: %w", ref, err)
	}
	defer res.Close()
	if err := res.Wait(ctx); err != nil {
		return fmt.Errorf("docker: pull %s: %w", ref, err)
	}
	return nil
}

func healthcheck(test []string) *container.HealthConfig {
	if test == nil {
		return nil
	}
	return &container.HealthConfig{
		Test: test, Interval: 2 * time.Second, Timeout: 5 * time.Second, Retries: 30, StartPeriod: 5 * time.Second,
	}
}

// TagImage gives the image src the additional name dst.
func (r *Runtime) TagImage(ctx context.Context, src, dst string) error {
	if _, err := r.c.ImageTag(ctx, client.ImageTagOptions{Source: src, Target: dst}); err != nil {
		return fmt.Errorf("docker: tag %s as %s: %w", src, dst, err)
	}
	return nil
}

// Exec runs cmd inside a running container, feeding it in (if not nil) as stdin.
func (r *Runtime) Exec(ctx context.Context, name string, cmd []string, in io.Reader, w io.Writer) (int, error) {
	ex, err := r.c.ExecCreate(ctx, name, client.ExecCreateOptions{Cmd: cmd, AttachStdin: in != nil, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return 0, fmt.Errorf("docker: exec in %s: %w", name, err)
	}
	att, err := r.c.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{})
	if err != nil {
		return 0, fmt.Errorf("docker: exec in %s: %w", name, err)
	}
	defer att.Close()
	if in != nil {
		go func() {
			io.Copy(att.Conn, in)
			if cw, ok := att.Conn.(interface{ CloseWrite() error }); ok {
				cw.CloseWrite() // EOF for the command
			}
		}()
	}
	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(w, &stderr, att.Reader); err != nil {
		return 0, fmt.Errorf("docker: exec in %s: %w", name, err)
	}
	res, err := r.c.ExecInspect(ctx, ex.ID, client.ExecInspectOptions{})
	if err != nil {
		return 0, fmt.Errorf("docker: exec in %s: %w", name, err)
	}
	if res.ExitCode != 0 {
		return res.ExitCode, fmt.Errorf("%s: %s exited with %d: %s", name, cmd[0], res.ExitCode, bytes.TrimSpace(stderr.Bytes()))
	}
	return 0, nil
}

// RemoveVolume deletes a volume and its data.
func (r *Runtime) RemoveVolume(ctx context.Context, name string) error {
	if _, err := r.c.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: remove volume %s: %w", name, err)
	}
	return nil
}

// Stats samples a container's CPU and memory over one second, the way `docker stats` does.
func (r *Runtime) Stats(ctx context.Context, name string) (deploy.Stats, error) {
	res, err := r.c.ContainerStats(ctx, name, client.ContainerStatsOptions{IncludePreviousSample: true})
	if err != nil {
		return deploy.Stats{}, fmt.Errorf("docker: stats %s: %w", name, err)
	}
	defer res.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return deploy.Stats{}, fmt.Errorf("docker: stats %s: %w", name, err)
	}
	return summarize(st), nil
}

// summarize turns a Docker sample into the numbers Kayly shows. Memory excludes the page
// cache the kernel can take back, as `docker stats` does, so it matches what an OOM kill sees.
func summarize(st container.StatsResponse) deploy.Stats {
	out := deploy.Stats{MemoryLimit: st.MemoryStats.Limit}
	cache := st.MemoryStats.Stats["inactive_file"]                // cgroup v2
	if v, ok := st.MemoryStats.Stats["total_inactive_file"]; ok { // cgroup v1
		cache = v
	}
	if st.MemoryStats.Usage > cache {
		out.MemoryBytes = st.MemoryStats.Usage - cache
	}
	cpu := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
	sys := float64(st.CPUStats.SystemUsage) - float64(st.PreCPUStats.SystemUsage)
	if cpu > 0 && sys > 0 {
		cores := float64(st.CPUStats.OnlineCPUs)
		if cores == 0 {
			cores = float64(len(st.CPUStats.CPUUsage.PercpuUsage))
		}
		out.CPUPercent = cpu / sys * cores * 100
	}
	return out
}

// Version returns the Docker Engine's version.
func (r *Runtime) Version(ctx context.Context) (string, error) {
	v, err := r.c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", fmt.Errorf("docker: version: %w", err)
	}
	return v.Version, nil
}
