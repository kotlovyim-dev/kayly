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
		return fmt.Errorf("${%s}: want ${KEY}, ${<add-on>.<field>} or ${<service>.url}", ref)
	case AddonFields[name] != nil:
		if !slices.Contains(AddonFields[name], field) {
			return fmt.Errorf("${%s}: %s fields are %s", ref, name, strings.Join(AddonFields[name], ", "))
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
// stored holds the values kept by Kayly: user-set and generated ones. refs holds add-on and
// service fields such as "postgres.url" and "app.url". injected holds the variables the
// attached add-ons provide, such as DATABASE_URL.
//
// Declared keys are delivered by their [env] rules. Injected keys that are not declared go
// to every target; declaring one (to scope it, or to template it) takes it over. Stored
// keys that are not declared are not delivered and are returned as ignored, so the caller
// can warn about them.
func (c *Config) ResolveEnv(stored, refs, injected map[string]string) (env map[string]map[string]string, ignored []string, err error) {
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
		for k, v := range injected {
			if _, declared := c.Env[k]; !declared {
				env[t][k] = v
			}
		}
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
			kind, _, _ := strings.Cut(ref, ".")
			if AddonFields[kind] != nil {
				errs = append(errs, fmt.Errorf("env.%s: ${%s}: the project has no %s add-on; add one with `kayly add %s`", key, ref, kind, kind))
			} else {
				errs = append(errs, fmt.Errorf("env.%s: ${%s} has no value", key, ref))
			}
		}
		return v
	})
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	values[key] = out
	return out, nil
}

// ExpandRefs resolves the ${name.field} references of tmpl from refs, as a menu button URL
// needs at the time it is applied.
func ExpandRefs(tmpl string, refs map[string]string) (string, error) {
	var missing []string
	out := refRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		v, ok := refs[m[2:len(m)-1]]
		if !ok {
			missing = append(missing, m)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s has no value", strings.Join(missing, ", "))
	}
	return out, nil
}
