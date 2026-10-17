package config

import (
	"errors"
	"maps"
	"strings"
	"testing"
)

// leryo is the config of the first real tenant (ARCHITECTURE.md §5).
const leryo = `
[build]
dockerfile = "Dockerfile"
target = "app"

[release]
command = "node src/db/migrate.ts"

[services.app]
command = "node src/server.ts"
port = 3000
health = "/api/ready"
volumes = ["uploads:/data/uploads"]
read_only = true

[services.worker]
command = "node src/worker.ts"
volumes = ["uploads:/data/uploads"]

[addons.postgres]
version = "18"

[env]
BETTER_AUTH_SECRET = { generate = "base64:32" }
DB_APP_PASSWORD    = { generate = "hex:32", to = [] }
DATABASE_URL       = { template = "postgres://leryo_app:${DB_APP_PASSWORD}@${postgres.host}:5432/leryo" }
DATABASE_ADMIN_URL = { template = "${postgres.url}", to = ["release"] }
WEB_ORIGIN         = { template = "${app.url}" }
OPENROUTER_API_KEY = { required = false }
SMTP_URL           = {}
`

func TestParseLeryo(t *testing.T) {
	c, err := Parse([]byte(leryo))
	if err != nil {
		t.Fatal(err)
	}
	app := c.Services["app"]
	if app.Port != 3000 || app.Health != "/api/ready" || !app.ReadOnly || !app.Overlaps() {
		t.Errorf("app = %+v", app)
	}
	if v := app.Volumes[0]; v.Name != "uploads" || v.Path != "/data/uploads" {
		t.Errorf("volume = %+v", v)
	}
	if got := strings.Join(c.Targets(), ","); got != "app,worker,release" {
		t.Errorf("targets = %s", got)
	}
}

func TestParseErrors(t *testing.T) {
	const svc = "[services.web]\nport = 80\n"
	tests := []struct{ name, toml, want string }{
		{"unknown key", svc + "colour = 1\n", "unknown key"},
		{"no services", "[build]\n", "no [services.<name>]"},
		{"bad service name", "[services.Web]\n", "name must match"},
		{"reserved release", "[services.release]\n", "reserved"},
		{"reserved addon", "[services.postgres]\n", "reserved"},
		{"port range", "[services.web]\nport = 70000\n", "out of range"},
		{"health without port", "[services.web]\nhealth = \"/h\"\n", "needs a port"},
		{"health not a path", svc + "health = \"h\"\n", "needs a port"},
		{"volume relative", svc + "volumes = [\"data:data\"]\n", "name:/absolute/path"},
		{"volume unclean", svc + "volumes = [\"data:/a/../b\"]\n", "name:/absolute/path"},
		{"volume twice", svc + "volumes = [\"a:/d\", \"b:/d\"]\n", "mounted twice"},
		{"target without dockerfile", svc + "[build]\ntarget = \"app\"\n", "needs build.dockerfile"},
		{"empty release", svc + "[release]\ncommand = \" \"\n", "release.command is empty"},
		{"postgres version", svc + "[addons.postgres]\n", "version is required"},
		{"env key", svc + "[env]\nlower = {}\n", "name must match"},
		{"generate format", svc + "[env]\nK = { generate = \"hex\" }\n", "want hex:N"},
		{"generate and template", svc + "[env]\nK = { generate = \"hex:8\", template = \"x\" }\n", "exclusive"},
		{"required on generated", svc + "[env]\nK = { generate = \"hex:8\", required = false }\n", "only applies"},
		{"unknown target", svc + "[env]\nK = { to = [\"api\"] }\n", "not a service"},
		{"undeclared ref", svc + "[env]\nK = { template = \"${X}\" }\n", "not declared"},
		{"bad ref", svc + "[env]\nK = { template = \"${x}\" }\n", "want ${KEY}"},
		{"ref without addon", svc + "[env]\nK = { template = \"${postgres.url}\" }\n", "no [addons.postgres]"},
		{"bad postgres field", svc + "[addons.postgres]\nversion = \"18\"\n[env]\nK = { template = \"${postgres.pass}\" }\n", "postgres fields are"},
		{"url of worker", "[services.w]\n[env]\nK = { template = \"${w.url}\" }\n", "only services with a port"},
		{"cycle", svc + "[env]\nA = { template = \"${B}\" }\nB = { template = \"${A}\" }\n", "template cycle A → B → A"},
		{"syntax", "[services.web\n", "kayly.toml:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.toml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestResolveEnvLeryo(t *testing.T) {
	c, err := Parse([]byte(leryo))
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]string{
		"BETTER_AUTH_SECRET": "s3cret",
		"DB_APP_PASSWORD":    "pw",
		"SMTP_URL":           "smtp://relay:587",
		"LEFTOVER":           "x",
	}
	refs := map[string]string{
		"postgres.host": "leryo-postgres",
		"postgres.url":  "postgres://postgres:admin@leryo-postgres:5432/postgres",
		"app.url":       "https://leryo.example.com",
	}
	env, ignored, err := c.ResolveEnv(stored, refs)
	if err != nil {
		t.Fatal(err)
	}
	common := map[string]string{
		"BETTER_AUTH_SECRET": "s3cret",
		"DATABASE_URL":       "postgres://leryo_app:pw@leryo-postgres:5432/leryo",
		"WEB_ORIGIN":         "https://leryo.example.com",
		"SMTP_URL":           "smtp://relay:587",
	}
	release := maps.Clone(common)
	release["DATABASE_ADMIN_URL"] = refs["postgres.url"]
	want := map[string]map[string]string{"app": common, "worker": common, "release": release}
	for target, w := range want {
		if !maps.Equal(env[target], w) {
			t.Errorf("%s:\n got %v\nwant %v", target, env[target], w)
		}
	}
	if len(ignored) != 1 || ignored[0] != "LEFTOVER" {
		t.Errorf("ignored = %v", ignored)
	}
}

func TestResolveEnvMissing(t *testing.T) {
	c, err := Parse([]byte(leryo))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.ResolveEnv(map[string]string{}, nil)
	var me *MissingError
	if !errors.As(err, &me) || strings.Join(me.Keys, ",") != "BETTER_AUTH_SECRET,DB_APP_PASSWORD,SMTP_URL" {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveEnvTemplates(t *testing.T) {
	c, err := Parse([]byte(`
[services.web]
[env]
OPT  = { required = false }
HOST = {}
URL  = { template = "https://${HOST}/${PATH}?o=${OPT}" }
PATH = { template = "v1" }
`))
	if err != nil {
		t.Fatal(err)
	}
	env, _, err := c.ResolveEnv(map[string]string{"HOST": "h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := env["web"]["URL"]; got != "https://h/v1?o=" {
		t.Errorf("URL = %q", got)
	}
	if _, ok := env["web"]["OPT"]; ok {
		t.Error("an optional variable without a value must not be delivered")
	}
}

func TestResolveEnvMissingRef(t *testing.T) {
	c, err := Parse([]byte("[services.web]\nport = 80\n[env]\nU = { template = \"${web.url}\" }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ResolveEnv(nil, nil); err == nil || !strings.Contains(err.Error(), "${web.url} has no value") {
		t.Fatalf("err = %v", err)
	}
}
