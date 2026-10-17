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
		{"reserved redis", "[services.redis]\n", "reserved"},
		{"port range", "[services.web]\nport = 70000\n", "out of range"},
		{"health without port", "[services.web]\nhealth = \"/h\"\n", "needs a port"},
		{"health not a path", svc + "health = \"h\"\n", "needs a port"},
		{"volume relative", svc + "volumes = [\"data:data\"]\n", "name:/absolute/path"},
		{"volume unclean", svc + "volumes = [\"data:/a/../b\"]\n", "name:/absolute/path"},
		{"volume twice", svc + "volumes = [\"a:/d\", \"b:/d\"]\n", "mounted twice"},
		{"dockerfile outside", svc + "[build]\ndockerfile = \"../Dockerfile\"\n", "inside the app"},
		{"dockerfile absolute", svc + "[build]\ndockerfile = \"/etc/passwd\"\n", "inside the app"},
		{"target without dockerfile", svc + "[build]\ntarget = \"app\"\n", "needs build.dockerfile"},
		{"empty release", svc + "[release]\ncommand = \" \"\n", "release.command is empty"},
		{"env key", svc + "[env]\nlower = {}\n", "name must match"},
		{"generate format", svc + "[env]\nK = { generate = \"hex\" }\n", "want hex:N"},
		{"generate and template", svc + "[env]\nK = { generate = \"hex:8\", template = \"x\" }\n", "exclusive"},
		{"required on generated", svc + "[env]\nK = { generate = \"hex:8\", required = false }\n", "only applies"},
		{"unknown target", svc + "[env]\nK = { to = [\"api\"] }\n", "not a service"},
		{"undeclared ref", svc + "[env]\nK = { template = \"${X}\" }\n", "not declared"},
		{"bad ref", svc + "[env]\nK = { template = \"${x}\" }\n", "want ${KEY}"},
		{"bad postgres field", svc + "[env]\nK = { template = \"${postgres.pass}\" }\n", "postgres fields are"},
		{"bad redis field", svc + "[env]\nK = { template = \"${redis.db}\" }\n", "redis fields are"},
		{"addons are not configured here", svc + "[addons.postgres]\nversion = \"18\"\n", "unknown key"},
		{"url of worker", "[services.w]\n[env]\nK = { template = \"${w.url}\" }\n", "only services with a port"},
		{"cycle", svc + "[env]\nA = { template = \"${B}\" }\nB = { template = \"${A}\" }\n", "template cycle A → B → A"},
		{"syntax", "[services.web\n", "kayly.toml:1"},
		{"port format", "[services.rtc]\nports = [\"7882\"]\n", "want <1-65535>/tcp"},
		{"port proto", "[services.rtc]\nports = [\"7882/sctp\"]\n", "want <1-65535>/tcp"},
		{"proxy port", "[services.rtc]\nports = [\"443/tcp\"]\n", "belongs to the proxy"},
		{"proxy http3", "[services.rtc]\nports = [\"443/udp\"]\n", "belongs to the proxy"},
		{"port twice", "[services.a]\nports = [\"3478/udp\"]\n[services.b]\nports = [\"3478/udp\"]\n", "published twice"},
		{"ports and overlap", "[services.rtc]\nports = [\"7881/tcp\"]\noverlap = true\n", "cannot overlap"},
		{"unterminated quote", "[services.web]\ncommand = \"sh -c 'x\"\n", "services.web.command"},
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
	// The add-on's DATABASE_URL is superuser; leryo declares its own, which wins.
	injected := map[string]string{"DATABASE_URL": refs["postgres.url"], "PGHOST": "leryo-postgres"}
	env, ignored, err := c.ResolveEnv(stored, refs, injected)
	if err != nil {
		t.Fatal(err)
	}
	common := map[string]string{
		"PGHOST":             "leryo-postgres",
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
	_, _, err = c.ResolveEnv(map[string]string{}, nil, nil)
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
	env, _, err := c.ResolveEnv(map[string]string{"HOST": "h"}, nil, nil)
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
	if _, _, err := c.ResolveEnv(nil, nil, nil); err == nil || !strings.Contains(err.Error(), "${web.url} has no value") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveEnvMissingAddon(t *testing.T) {
	c, err := Parse([]byte("[services.web]\n[env]\nU = { template = \"${redis.url}\" }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ResolveEnv(nil, nil, nil); err == nil || !strings.Contains(err.Error(), "kayly add redis") {
		t.Fatalf("err = %v", err)
	}
}

func TestInjectedEnvGoesEverywhereUnlessDeclared(t *testing.T) {
	c, err := Parse([]byte("[release]\ncommand = \"m\"\n[services.web]\n[services.worker]\n[env]\nREDIS_URL = { template = \"${redis.url}\", to = [\"worker\"] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	injected := map[string]string{"REDIS_URL": "redis://r", "DATABASE_URL": "postgres://p"}
	env, _, err := c.ResolveEnv(nil, map[string]string{"redis.url": "redis://r"}, injected)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"web", "worker", "release"} {
		if env[target]["DATABASE_URL"] != "postgres://p" {
			t.Errorf("%s: DATABASE_URL missing", target)
		}
	}
	if _, ok := env["web"]["REDIS_URL"]; ok {
		t.Error("a declared key reached a target outside its `to`")
	}
	if env["worker"]["REDIS_URL"] != "redis://r" {
		t.Error("worker: REDIS_URL missing")
	}
}

func TestHostPorts(t *testing.T) {
	c, err := Parse([]byte("[services.rtc]\nport = 7880\nports = [\"7881/tcp\", \"7882/udp\", \"3478/udp\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	rtc := c.Services["rtc"]
	if len(rtc.Ports) != 3 || rtc.Ports[1] != (HostPort{7882, "udp"}) {
		t.Errorf("ports = %v", rtc.Ports)
	}
	if rtc.Overlaps() {
		t.Error("a service with host ports must not overlap")
	}
}
