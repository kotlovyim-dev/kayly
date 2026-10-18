// Package config parses and validates kayly.toml, the file in an app repository that
// describes how Kayly builds and runs the app. See ARCHITECTURE.md §5.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/pelletier/go-toml/v2"
)

// FileName is the config file Kayly looks for at the root of an app.
const FileName = "kayly.toml"

// ReleaseTarget is the env target name of the release step. No service may use it.
const ReleaseTarget = "release"

// Config is a validated kayly.toml.
type Config struct {
	Build    Build              `toml:"build"`
	Release  *Release           `toml:"release"`
	Services map[string]Service `toml:"services"`
	Env      map[string]EnvVar  `toml:"env"`
}

// Build selects how the image is built. An empty Dockerfile means Railpack.
type Build struct {
	Dockerfile string `toml:"dockerfile"`
	Target     string `toml:"target"`
}

// Release is a one-off command run from the new image before any service of it starts.
type Release struct {
	Command string `toml:"command"`
}

// Service is one long-running process of the app.
type Service struct {
	// Command overrides the image's default command. Empty keeps the image default.
	Command string `toml:"command"`
	// Port the service listens on. Zero means the service takes no traffic (a worker).
	Port int `toml:"port"`
	// Health is an HTTP path checked before traffic switches. Empty means a TCP check.
	Health  string   `toml:"health"`
	Volumes []Volume `toml:"volumes"`
	// Overlap lets the old and the new container run side by side during a deploy.
	// Nil means true. False stops the old one first (singletons, polling bots).
	Overlap *bool `toml:"overlap"`
	// ReadOnly mounts the root filesystem read-only, with a tmpfs on /tmp.
	ReadOnly bool `toml:"read_only"`
	// Ports are published on the host at the same numbers, for traffic the HTTP proxy cannot
	// carry (media, TURN, game servers). A service with ports never overlaps: two containers
	// cannot bind one host port.
	Ports []HostPort `toml:"ports"`
	// Memory caps the service's RAM, e.g. "512m"; swap is off. Empty means unlimited.
	Memory ByteSize `toml:"memory"`
	// CPUs caps the service's CPU time in cores, e.g. 1.5. Zero means unlimited.
	CPUs float64 `toml:"cpus"`
	// Telegram makes the service a Telegram bot (ARCHITECTURE.md §6).
	Telegram *Telegram `toml:"telegram"`
}

// WebhookSecretKey is the env key Kayly generates for webhook bots and delivers to them; a
// bot checks it against the X-Telegram-Bot-Api-Secret-Token header.
const WebhookSecretKey = "TELEGRAM_WEBHOOK_SECRET"

// Telegram configures a service as a Telegram bot.
type Telegram struct {
	// Token is the [env] key that holds the bot token. It must reach the service.
	Token string `toml:"token"`
	// Mode is "webhook" (Telegram posts updates to Path) or "polling" (the bot asks for them).
	Mode string `toml:"mode"`
	// Path is where a webhook bot takes updates, e.g. "/telegram".
	Path string `toml:"path"`
	// MenuButton opens a Mini App from the chat. Nil leaves the bot's menu button alone.
	MenuButton *MenuButton `toml:"menu_button"`
}

// MenuButton is a web_app menu button.
type MenuButton struct {
	Text string `toml:"text"`
	// URL may use ${<service>.url}.
	URL string `toml:"url"`
}

// ByteSize is an amount of memory written as a number and a unit: "256m", "1.5g", "2048k".
type ByteSize int64

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([kmg])$`)

// UnmarshalText parses "<number><k|m|g>" (powers of 1024). Docker refuses less than 6 MiB.
func (b *ByteSize) UnmarshalText(text []byte) error {
	m := sizeRe.FindStringSubmatch(strings.ToLower(string(text)))
	if m == nil {
		return fmt.Errorf("size %q: want a number and k, m or g, e.g. 512m", text)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	n *= float64(int64(1) << (10 * (1 + strings.Index("kmg", m[2]))))
	if n < 6<<20 || n > 1<<50 {
		return fmt.Errorf("size %q: must be between 6m and 1048576g", text)
	}
	*b = ByteSize(n)
	return nil
}

// Overlaps reports whether old and new containers may run at the same time.
// Two polling bots with one token get 409 Conflict, so a polling bot never overlaps either.
func (s Service) Overlaps() bool {
	return len(s.Ports) == 0 && !s.polls() && (s.Overlap == nil || *s.Overlap)
}

func (s Service) polls() bool { return s.Telegram != nil && s.Telegram.Mode == "polling" }

// HostPort is a port published on every host interface, e.g. "7882/udp".
type HostPort struct {
	Port  int
	Proto string
}

// UnmarshalText parses "<port>/<tcp|udp>".
func (p *HostPort) UnmarshalText(b []byte) error {
	num, proto, ok := strings.Cut(string(b), "/")
	n, err := strconv.Atoi(num)
	if !ok || err != nil || n < 1 || n > 65535 || (proto != "tcp" && proto != "udp") {
		return fmt.Errorf("port %q: want <1-65535>/tcp or <1-65535>/udp", b)
	}
	p.Port, p.Proto = n, proto
	return nil
}

func (p HostPort) String() string { return strconv.Itoa(p.Port) + "/" + p.Proto }

// Volume is a named, project-scoped volume mounted into a service.
type Volume struct {
	Name string
	Path string
}

// UnmarshalText parses "name:/absolute/path".
func (v *Volume) UnmarshalText(b []byte) error {
	name, p, ok := strings.Cut(string(b), ":")
	if !ok || !nameRe.MatchString(name) || !path.IsAbs(p) || path.Clean(p) != p {
		return fmt.Errorf("volume %q: want name:/absolute/path, name in [a-z0-9-]", b)
	}
	v.Name, v.Path = name, p
	return nil
}

// EnvVar declares one environment variable. At most one of Generate and Template is set;
// with neither, the value is set by the user in Kayly.
type EnvVar struct {
	// Generate is "hex:N" or "base64:N" (N random bytes), created once on first deploy.
	Generate string `toml:"generate"`
	// Template is resolved at release time from ${KEY} and ${name.field} references.
	Template string `toml:"template"`
	// Required is nil for true. Only user-set values can be optional.
	Required *bool `toml:"required"`
	// To lists the targets that receive the variable. Nil means all; empty means none
	// (the variable only feeds templates).
	To []string `toml:"to"`
}

// IsRequired reports whether a deploy must fail when the variable has no value.
func (e EnvVar) IsRequired() bool { return e.Required == nil || *e.Required }

// Load reads and validates the config in dir.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(path.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and validates a kayly.toml. Unknown keys are errors.
func Parse(b []byte) (*Config, error) {
	var c Config
	dec := toml.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return nil, fmt.Errorf("%s: unknown key\n%s", FileName, strict.String())
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, col := de.Position()
			return nil, fmt.Errorf("%s:%d:%d: %s", FileName, row, col, de.Error())
		}
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if bots := c.webhookBots(); len(bots) > 0 {
		if c.Env == nil {
			c.Env = map[string]EnvVar{}
		}
		c.Env[WebhookSecretKey] = EnvVar{Generate: "hex:32", To: bots}
	}
	return &c, nil
}

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	genRe    = regexp.MustCompile(`^(hex|base64):([1-9][0-9]*)$`)
)

// ValidName reports whether name can name a project, service or volume.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// AddonFields lists, per add-on kind, the ${<kind>.<field>} references a template may use.
// Add-ons are attached to a project in Kayly (`kayly add <kind>`), not in this file; their
// kinds are reserved as service names because templates use them as namespaces.
var AddonFields = map[string][]string{
	"postgres": {"host", "port", "user", "password", "db", "url"},
	"redis":    {"host", "port", "password", "url"},
}

func (c *Config) validate() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if c.Build.Target != "" && c.Build.Dockerfile == "" {
		fail("build.target needs build.dockerfile")
	}
	if d := c.Build.Dockerfile; d != "" && !filepath.IsLocal(d) {
		fail("build.dockerfile: %q must be a path inside the app", d)
	}
	if c.Release != nil {
		if strings.TrimSpace(c.Release.Command) == "" {
			fail("release.command is empty")
		} else if err := checkCommand(c.Release.Command); err != nil {
			fail("release.command: %v", err)
		}
	}
	if len(c.Services) == 0 {
		fail("no [services.<name>] defined")
	}
	published := map[HostPort]bool{}
	for _, name := range slices.Sorted(maps.Keys(c.Services)) {
		s := c.Services[name]
		switch {
		case !nameRe.MatchString(name):
			fail("services.%s: name must match %s", name, nameRe)
		case name == ReleaseTarget || AddonFields[name] != nil:
			fail("services.%s: name is reserved", name)
		}
		if err := checkCommand(s.Command); err != nil {
			fail("services.%s.command: %v", name, err)
		}
		if s.Port < 0 || s.Port > 65535 {
			fail("services.%s.port: %d is out of range", name, s.Port)
		}
		if s.Health != "" && (s.Port == 0 || !strings.HasPrefix(s.Health, "/")) {
			fail("services.%s.health: needs a port and a path starting with /", name)
		}
		seen := map[string]bool{}
		for _, v := range s.Volumes {
			if seen[v.Path] {
				fail("services.%s.volumes: %s mounted twice", name, v.Path)
			}
			seen[v.Path] = true
		}
		if s.Memory != 0 && s.Memory < 6<<20 { // a bare TOML number skips UnmarshalText
			fail("services.%s.memory: write it as a string with a unit, e.g. \"512m\"", name)
		}
		if s.CPUs < 0 || s.CPUs > 1024 {
			fail("services.%s.cpus: %v is out of range; use a number of cores such as 0.5 or 2", name, s.CPUs)
		}
		if len(s.Ports) > 0 && s.Overlap != nil && *s.Overlap {
			fail("services.%s.overlap: a service with host ports cannot overlap", name)
		}
		for _, p := range s.Ports {
			switch {
			case p.Port == 80 || p.Port == 443: // 443/udp carries the proxy's HTTP/3
				fail("services.%s.ports: %s belongs to the proxy; use port and a domain", name, p)
			case published[p]:
				fail("services.%s.ports: %s is published twice", name, p)
			}
			published[p] = true
		}
		if s.Telegram != nil {
			c.checkTelegram(name, s, fail)
		}
	}
	if _, ok := c.Env[WebhookSecretKey]; ok && len(c.webhookBots()) > 0 {
		fail("env.%s: Kayly generates it for webhook bots; remove the line", WebhookSecretKey)
	}
	for _, key := range slices.Sorted(maps.Keys(c.Env)) {
		e := c.Env[key]
		where := "env." + key
		if !envKeyRe.MatchString(key) {
			fail("%s: name must match %s", where, envKeyRe)
		}
		if e.Generate != "" && e.Template != "" {
			fail("%s: generate and template are exclusive", where)
		}
		if e.Generate != "" && !genRe.MatchString(e.Generate) {
			fail("%s.generate: want hex:N or base64:N", where)
		}
		if e.Required != nil && (e.Generate != "" || e.Template != "") {
			fail("%s.required: only applies to values set by the user", where)
		}
		if e.Template != "" {
			for _, ref := range refs(e.Template) {
				if err := c.checkRef(ref); err != nil {
					fail("%s.template: %v", where, err)
				}
			}
		}
		c.checkTargets(where+".to", e.To, fail)
	}
	if err := c.checkTemplateCycles(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// checkCommand rejects commands that do not split into words. Commands run without a
// shell; use `sh -c '...'` for pipes, && and variable expansion.
func checkCommand(cmd string) error {
	_, err := shellquote.Split(cmd)
	return err
}

func (c *Config) checkTargets(where string, targets []string, fail func(string, ...any)) {
	for _, t := range targets {
		if _, ok := c.Services[t]; !ok && t != ReleaseTarget {
			fail("%s: %q is not a service or %q", where, t, ReleaseTarget)
		}
	}
}

func (c *Config) checkTelegram(name string, s Service, fail func(string, ...any)) {
	t, where := s.Telegram, "services."+name+".telegram"
	if e, ok := c.Env[t.Token]; !ok {
		fail("%s.token: name the [env] key that holds the bot token", where)
	} else if e.To != nil && !slices.Contains(e.To, name) {
		fail("%s.token: env.%s does not reach %s; add it to its `to`", where, t.Token, name)
	}
	switch t.Mode {
	case "webhook":
		if s.Port == 0 {
			fail("%s: a webhook bot needs a port", where)
		}
		if !strings.HasPrefix(t.Path, "/") {
			fail("%s.path: a webhook bot needs a path starting with /, e.g. \"/telegram\"", where)
		}
	case "polling":
		if t.Path != "" {
			fail("%s.path: only webhook bots take a path", where)
		}
		if s.Overlap != nil && *s.Overlap {
			fail("%s: a polling bot cannot overlap: two getUpdates clients conflict", where)
		}
	default:
		fail("%s.mode: want \"webhook\" or \"polling\"", where)
	}
	if m := t.MenuButton; m != nil {
		if m.Text == "" {
			fail("%s.menu_button.text is empty", where)
		}
		if !strings.HasPrefix(m.URL, "https://") && !strings.HasPrefix(m.URL, "${") {
			fail("%s.menu_button.url: a Mini App needs an https:// URL", where)
		}
		for _, ref := range refs(m.URL) {
			svc, field, _ := strings.Cut(ref, ".")
			if target, ok := c.Services[svc]; !ok || field != "url" || target.Port == 0 {
				fail("%s.menu_button.url: ${%s}: only ${<service>.url} of a service with a port", where, ref)
			}
		}
	}
}

// webhookBots returns the services that take Telegram updates by webhook, sorted.
func (c *Config) webhookBots() []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(c.Services)) {
		if t := c.Services[name].Telegram; t != nil && t.Mode == "webhook" {
			out = append(out, name)
		}
	}
	return out
}
