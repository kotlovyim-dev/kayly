package deploy

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

// fakeRuntime is an in-memory container engine. A container's IP is its own name, so a
// health probe can tell which container it hits.
type fakeRuntime struct {
	mu         sync.Mutex
	containers map[string]*fakeContainer
	images     map[string]bool
	networks   map[string]bool
	volumes    map[string]bool
	removed    []string // every container name passed to Remove, in order

	// Failure injection, keyed by container name (or image for builds).
	crashOnStart map[string]bool // starts, then is found exited with code 1
	exitCode     map[string]int  // exit code for one-off containers
	unhealthy    map[string]bool // health probe fails
	// hook runs at the start of every call with the method name and argument; an error is
	// returned from the call. Tests use it to fail or block at a chosen point.
	hook func(method, arg string) error
}

type fakeContainer struct {
	spec    Spec
	running bool
	exit    int
	health  string
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		containers: map[string]*fakeContainer{}, images: map[string]bool{},
		networks: map[string]bool{}, volumes: map[string]bool{},
		crashOnStart: map[string]bool{}, exitCode: map[string]int{}, unhealthy: map[string]bool{},
	}
}

func (f *fakeRuntime) call(method, arg string) error {
	f.mu.Lock()
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		return hook(method, arg)
	}
	return nil
}

func (f *fakeRuntime) EnsureNetwork(_ context.Context, name string) error {
	if err := f.call("EnsureNetwork", name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[name] = true
	return nil
}

func (f *fakeRuntime) EnsureVolume(_ context.Context, name string) error {
	if err := f.call("EnsureVolume", name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumes[name] = true
	return nil
}

func (f *fakeRuntime) Run(_ context.Context, spec Spec) error {
	if err := f.call("Run", spec.Name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.containers[spec.Name]; ok {
		return fmt.Errorf("container name %s is taken", spec.Name)
	}
	if !f.images[spec.Image] {
		return fmt.Errorf("no image %s", spec.Image)
	}
	for _, n := range spec.Networks {
		if !f.networks[n.Name] {
			return fmt.Errorf("no network %s", n.Name)
		}
	}
	for _, m := range spec.Mounts {
		if !f.volumes[m.Volume] {
			return fmt.Errorf("no volume %s", m.Volume)
		}
	}
	c := &fakeContainer{spec: spec, running: true}
	if spec.Health != nil {
		c.health = "healthy"
	}
	if !spec.Restart { // a one-off run finishes at once
		c.running, c.exit = false, f.exitCode[spec.Name]
	}
	if f.crashOnStart[spec.Name] {
		c.running, c.exit = false, 1
	}
	f.containers[spec.Name] = c
	return nil
}

func (f *fakeRuntime) Wait(_ context.Context, name string) (int, error) {
	if err := f.call("Wait", name); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[name]
	if !ok {
		return 0, ErrNoContainer
	}
	return c.exit, nil
}

func (f *fakeRuntime) Logs(_ context.Context, name string, _ bool, _ int, w io.Writer) error {
	if err := f.call("Logs", name); err != nil {
		return err
	}
	fmt.Fprintf(w, "[%s output]\n", name)
	return nil
}

func (f *fakeRuntime) Inspect(_ context.Context, name string) (Container, error) {
	if err := f.call("Inspect", name); err != nil {
		return Container{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[name]
	if !ok {
		return Container{}, ErrNoContainer
	}
	return f.view(name, c), nil
}

func (f *fakeRuntime) view(name string, c *fakeContainer) Container {
	v := Container{
		Name: name, Labels: c.spec.Labels, Running: c.running, ExitCode: c.exit,
		StartedAt: time.Now().Add(-time.Hour), Health: c.health,
	}
	for _, n := range c.spec.Networks {
		if n.Name == EdgeNetwork {
			v.IP = name
		}
	}
	return v
}

func (f *fakeRuntime) Remove(_ context.Context, name string, _ time.Duration) error {
	if err := f.call("Remove", name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, name)
	delete(f.containers, name)
	return nil
}

func (f *fakeRuntime) List(context.Context) ([]Container, error) {
	if err := f.call("List", ""); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Container
	for _, name := range slices.Sorted(maps.Keys(f.containers)) {
		out = append(out, f.view(name, f.containers[name]))
	}
	return out, nil
}

func (f *fakeRuntime) ImageExists(_ context.Context, ref string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[ref], nil
}

func (f *fakeRuntime) RemoveImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.images, ref)
	return nil
}

// Build implements Builder: it "builds" by registering the image.
func (f *fakeRuntime) Build(_ context.Context, spec BuildSpec, w io.Writer) error {
	if err := f.call("Build", spec.Image); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images[spec.Image] = true
	fmt.Fprintf(w, "built %s\n", spec.Image)
	return nil
}

// health is the Deployer's checkHealth for the fake: addr's host is the container name.
func (f *fakeRuntime) health(_ context.Context, addr, _ string) error {
	host, _, _ := net.SplitHostPort(addr)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unhealthy[host] {
		return fmt.Errorf("503 from %s", host)
	}
	return nil
}

// names returns the names of all containers, sorted.
func (f *fakeRuntime) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.containers))
}

func (f *fakeRuntime) env(name string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.containers[name]; ok {
		return c.spec.Env
	}
	return nil
}

func (f *fakeRuntime) wasRemoved(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.removed, name)
}

// fakeProxy records the last loaded config.
type fakeProxy struct {
	mu   sync.Mutex
	last string
	hook func() error
}

func (p *fakeProxy) Load(_ context.Context, cfg []byte) error {
	p.mu.Lock()
	hook := p.hook
	p.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last = string(cfg)
	return nil
}

// routesTo reports whether the last config sends traffic to upstream.
func (p *fakeProxy) routesTo(upstream string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Contains(p.last, `"dial": "`+upstream+`"`)
}

func (f *fakeRuntime) TagImage(_ context.Context, src, dst string) error {
	if err := f.call("TagImage", dst); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[src] {
		return fmt.Errorf("no image %s", src)
	}
	f.images[dst] = true
	return nil
}

func (f *fakeRuntime) EnsureContainer(ctx context.Context, spec Spec) error {
	if err := f.call("EnsureContainer", spec.Name); err != nil {
		return err
	}
	f.mu.Lock()
	c, ok := f.containers[spec.Name]
	if ok && c.running {
		f.mu.Unlock()
		return nil
	}
	delete(f.containers, spec.Name)
	f.images[spec.Image] = true // pulled
	f.mu.Unlock()
	return f.Run(ctx, spec)
}

// Exec answers pg_dump with a fake dump.
func (f *fakeRuntime) Exec(_ context.Context, name string, cmd []string, w io.Writer) (int, error) {
	if err := f.call("Exec", name); err != nil {
		return 1, err
	}
	f.mu.Lock()
	_, ok := f.containers[name]
	f.mu.Unlock()
	if !ok {
		return 0, ErrNoContainer
	}
	fmt.Fprintf(w, "dump of %s by %s", name, cmd[0])
	return 0, nil
}

func (f *fakeRuntime) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, name)
	return nil
}

// serviceNames returns the containers that are not add-ons, sorted.
func (f *fakeRuntime) serviceNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, name := range slices.Sorted(maps.Keys(f.containers)) {
		if f.containers[name].spec.Labels[LabelRole] != RoleAddon {
			out = append(out, name)
		}
	}
	return out
}
