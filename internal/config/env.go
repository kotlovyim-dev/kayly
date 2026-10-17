package config

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var refRe = regexp.MustCompile(`\$\{([^}]*)\}`)

// PostgresFields are the ${postgres.<field>} references a template may use.
var PostgresFields = []string{"host", "port", "user", "password", "db", "url"}

// refs returns the names inside ${...} in order of appearance.
func refs(tmpl string) []string {
	var out []string
	for _, m := range refRe.FindAllStringSubmatch(tmpl, -1) {
		out = append(out, m[1])
	}
	return out
}

func (c *Config) checkRef(ref string) error {
	if envKeyRe.MatchString(ref) {
		if _, ok := c.Env[ref]; !ok {
			return fmt.Errorf("${%s}: not declared in [env]", ref)
		}
		return nil
	}
	name, field, ok := strings.Cut(ref, ".")
	switch {
	case !ok:
		return fmt.Errorf("${%s}: want ${KEY}, ${postgres.<field>} or ${<service>.url}", ref)
	case name == "postgres":
		if c.Addons.Postgres == nil {
			return fmt.Errorf("${%s}: no [addons.postgres]", ref)
		}
		if !slices.Contains(PostgresFields, field) {
			return fmt.Errorf("${%s}: postgres fields are %s", ref, strings.Join(PostgresFields, ", "))
		}
	default:
		s, ok := c.Services[name]
		if !ok {
			return fmt.Errorf("${%s}: no service %q", ref, name)
		}
		if field != "url" || s.Port == 0 {
			return fmt.Errorf("${%s}: only services with a port have a url", ref)
		}
	}
	return nil
}

func (c *Config) checkTemplateCycles() error {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(key string, chain []string) error
	visit = func(key string, chain []string) error {
		switch state[key] {
		case visiting:
			return fmt.Errorf("env: template cycle %s", strings.Join(append(chain, key), " → "))
		case done:
			return nil
		}
		state[key] = visiting
		for _, r := range refs(c.Env[key].Template) {
			if _, ok := c.Env[r]; ok {
				if err := visit(r, append(chain, key)); err != nil {
					return err
				}
			}
		}
		state[key] = done
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(c.Env)) {
		if err := visit(key, nil); err != nil {
			return err
		}
	}
	return nil
}

// Targets returns the env targets in a stable order: services by name, then the release step.
func (c *Config) Targets() []string {
	t := slices.Sorted(maps.Keys(c.Services))
	if c.Release != nil {
		t = append(t, ReleaseTarget)
	}
	return t
}

// MissingError lists required variables that have no value.
type MissingError struct{ Keys []string }

func (e *MissingError) Error() string {
	return "env: no value for " + strings.Join(e.Keys, ", ")
}

// ResolveEnv computes the environment of every target.
//
// stored holds the values kept by Kayly: user-set and generated ones. refs holds addon and
// service fields such as "postgres.url" and "app.url". Only keys declared in [env] are
// delivered, each only to its targets. Stored keys that are not declared are returned as
// ignored, so the caller can warn about them.
func (c *Config) ResolveEnv(stored, refs map[string]string) (env map[string]map[string]string, ignored []string, err error) {
	values := map[string]string{}
	var missing []string
	for key, e := range c.Env {
		v, ok := stored[key]
		switch {
		case e.Template != "":
		case ok:
			values[key] = v
		case e.Generate != "" || e.IsRequired():
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, nil, &MissingError{Keys: missing}
	}
	for key := range stored {
		if _, ok := c.Env[key]; !ok {
			ignored = append(ignored, key)
		}
	}
	sort.Strings(ignored)

	for key, e := range c.Env {
		if e.Template == "" {
			continue
		}
		v, err := c.expand(key, values, refs)
		if err != nil {
			return nil, nil, err
		}
		values[key] = v
	}

	env = map[string]map[string]string{}
	for _, t := range c.Targets() {
		env[t] = map[string]string{}
	}
	for key, v := range values {
		to := c.Env[key].To
		if to == nil {
			to = c.Targets()
		}
		for _, t := range to {
			env[t][key] = v
		}
	}
	return env, ignored, nil
}

// expand resolves the template of key, memoizing into values. Cycles are rejected by Parse.
func (c *Config) expand(key string, values, refs map[string]string) (string, error) {
	if v, ok := values[key]; ok {
		return v, nil
	}
	var errs []error
	out := refRe.ReplaceAllStringFunc(c.Env[key].Template, func(m string) string {
		ref := m[2 : len(m)-1]
		if _, declared := c.Env[ref]; declared {
			if c.Env[ref].Template != "" {
				v, err := c.expand(ref, values, refs)
				errs = append(errs, err)
				return v
			}
			// An optional variable without a value expands to "".
			return values[ref]
		}
		v, ok := refs[ref]
		if !ok {
			errs = append(errs, fmt.Errorf("env.%s: ${%s} has no value", key, ref))
		}
		return v
	})
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	values[key] = out
	return out, nil
}
