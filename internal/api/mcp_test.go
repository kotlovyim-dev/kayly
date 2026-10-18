package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kotlovyim-dev/kayly/internal/store"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpSession connects an MCP client to the env's public listener with token.
func (e *env) mcpSession(token string) *mcp.ClientSession {
	e.t.Helper()
	srv := httptest.NewServer(e.public)
	e.t.Cleanup(srv.Close)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { cs.Close() })
	return cs
}

func (e *env) token(body string) string {
	e.t.Helper()
	_, out := e.do(e.trusted, "POST", "/api/v1/tokens", body, "")
	var tv TokenView
	json.Unmarshal([]byte(out), &tv)
	return tv.Token
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		m[tool.Name] = tool
	}
	return m
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestMCPToolsFollowTheTokenScope(t *testing.T) {
	e := newEnv(t)
	read := toolNames(t, e.mcpSession(e.token(`{"name":"r","scope":"read"}`)))
	depSession := e.mcpSession(e.token(`{"name":"d","scope":"deploy"}`))
	dep := toolNames(t, depSession)
	// The goal of the milestone: a deploy token is refused remove_addon.
	if res, err := depSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "remove_addon", Arguments: map[string]any{"project": "app", "kind": "postgres", "confirm": "app"},
	}); err == nil && !res.IsError {
		t.Error("a deploy token called remove_addon")
	}
	admin := toolNames(t, e.mcpSession(e.token(`{"name":"a","scope":"admin"}`)))

	for _, n := range []string{"list_projects", "get_logs", "validate_config"} {
		if read[n] == nil {
			t.Errorf("read token lacks %s", n)
		}
	}
	for _, n := range []string{"deploy", "rollback", "set_env", "add_domain"} {
		if read[n] != nil || dep[n] == nil {
			t.Errorf("%s: read has it = %v, deploy has it = %v", n, read[n] != nil, dep[n] != nil)
		}
	}
	for _, n := range []string{"add_addon", "remove_addon", "restore_backup", "delete_project"} {
		if dep[n] != nil || admin[n] == nil {
			t.Errorf("%s: deploy has it = %v, admin has it = %v", n, dep[n] != nil, admin[n] != nil)
		}
	}
	for _, n := range []string{"remove_addon", "restore_backup", "delete_project", "delete_env"} {
		if h := admin[n].Annotations; h == nil || h.DestructiveHint == nil || !*h.DestructiveHint {
			t.Errorf("%s is not marked destructive", n)
		}
	}
	if !read["list_projects"].Annotations.ReadOnlyHint {
		t.Error("list_projects is not read-only")
	}
}

func TestMCPEnvIsWriteOnlyAndAudited(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"app", "other"} {
		e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"`+n+`"}`, "")
	}
	cs := e.mcpSession(e.token(`{"name":"agent","scope":"deploy","project":"app"}`))

	if res := call(t, cs, "set_env", map[string]any{"project": "app", "key": "API_KEY", "value": "hunter2"}); res.IsError {
		t.Fatalf("set_env: %s", text(res))
	}
	res := call(t, cs, "list_env", map[string]any{"project": "app"})
	if res.IsError || !strings.Contains(text(res), "API_KEY") || strings.Contains(text(res), "hunter2") {
		t.Errorf("list_env = %s", text(res))
	}
	if res := call(t, cs, "set_env", map[string]any{"project": "app", "key": "bad key", "value": "x"}); !res.IsError {
		t.Error("an invalid env key was accepted")
	}
	// Another project is off limits, and a project list shows only the bound one.
	if res := call(t, cs, "set_env", map[string]any{"project": "other", "key": "A", "value": "x"}); !res.IsError || !strings.Contains(text(res), "limited to project app") {
		t.Errorf("bound token reached another project: %s", text(res))
	}
	if out := text(call(t, cs, "list_projects", nil)); strings.Contains(out, "other") || !strings.Contains(out, "app") {
		t.Errorf("list_projects = %s", out)
	}

	_, log := e.do(e.trusted, "GET", "/api/v1/audit", "", "")
	if !strings.Contains(log, `"action":"mcp set_env"`) || !strings.Contains(log, `"token":"agent"`) {
		t.Errorf("audit log = %s", log)
	}
}

func TestMCPDestructiveToolsNeedTheProjectName(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")
	cs := e.mcpSession(e.token(`{"name":"boss","scope":"admin"}`))
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"delete_project", map[string]any{"project": "app"}},
		{"delete_project", map[string]any{"project": "app", "confirm": "yes"}},
		{"remove_addon", map[string]any{"project": "app", "kind": "redis"}},
		{"restore_backup", map[string]any{"project": "app", "name": "latest"}},
	} {
		if res := call(t, cs, c.tool, c.args); !res.IsError || !strings.Contains(text(res), "confirm") {
			t.Errorf("%s %v = %v %s", c.tool, c.args, res.IsError, text(res))
		}
	}
	if _, err := e.server.store.Project(context.Background(), "app"); err != nil {
		t.Errorf("project deleted without confirmation: %v", err)
	}
}

func TestMCPValidateConfig(t *testing.T) {
	e := newEnv(t)
	cs := e.mcpSession(e.token(`{"name":"r","scope":"read"}`))
	ok := call(t, cs, "validate_config", map[string]any{"toml": "[services.web]\nport = 3000\n"})
	if ok.IsError || !strings.Contains(text(ok), `"valid":true`) {
		t.Errorf("valid config: %s", text(ok))
	}
	bad := call(t, cs, "validate_config", map[string]any{"toml": "[services.web]\nprt = 3000\n"})
	if bad.IsError || !strings.Contains(text(bad), `"valid":false`) || !strings.Contains(text(bad), "2| prt") {
		t.Errorf("bad config: %s", text(bad))
	}
}

func TestStartReleaseReportsTheReleaseAtOnce(t *testing.T) {
	e := newEnv(t)
	s := e.server
	p, _ := s.store.CreateProject(context.Background(), "app")
	rel, _ := s.store.CreateRelease(context.Background(), p.ID, []byte("[services.web]"), store.Commit{})

	runUntil := func(release chan struct{}) func(context.Context, func(int)) error {
		return func(_ context.Context, notify func(int)) error {
			notify(rel.Seq)
			<-release
			return errors.New("boom")
		}
	}
	release := make(chan struct{})
	out, err := s.startRelease(context.Background(), p, false, runUntil(release))
	if err != nil || out.Release != rel.Seq || out.Done || out.Status != store.StatusBuilding {
		t.Fatalf("before the pipeline ends: %+v, %v", out, err)
	}
	close(release)

	// With wait the outcome comes back, including a failure with no recorded error.
	finished := make(chan struct{})
	close(finished)
	out, err = s.startRelease(context.Background(), p, true, runUntil(finished))
	if err != nil || !out.Done || out.Status != "failed" || out.Error != "boom" {
		t.Errorf("after the pipeline fails: %+v, %v", out, err)
	}
}

func TestMCPProjectToolsNeedARealProject(t *testing.T) {
	e := newEnv(t)
	e.do(e.trusted, "POST", "/api/v1/projects", `{"name":"app"}`, "")
	cs := e.mcpSession(e.token(`{"name":"agent","scope":"deploy","project":"app"}`))
	for _, args := range []map[string]any{{}, {"project": ""}, {"project": "ghost"}, {"project": "../app"}} {
		for _, tool := range []string{"list_backups", "create_backup", "get_logs", "restart_service"} {
			if res := call(t, cs, tool, args); !res.IsError {
				t.Errorf("%s %v succeeded", tool, args)
			}
		}
	}
}
