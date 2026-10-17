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
}

// Overlaps reports whether old and new containers may run at the same time.
func (s Service) Overlaps() bool { return s.Overlap == nil || *s.Overlap }

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
