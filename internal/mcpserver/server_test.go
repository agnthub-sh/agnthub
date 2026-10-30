package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/heynemann/agnthub/internal/mcpserver"
)

// fakeAPI serves canned registry API responses by path and records the last query.
type fakeAPI struct {
	responses map[string]response
	lastQuery atomic.Pointer[string]
}

type response struct {
	status int
	body   string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.RawQuery
	f.lastQuery.Store(&query)
	resp, ok := f.responses[r.URL.Path]
	if !ok {
		resp = response{http.StatusNotFound, `{"status":404,"detail":"repository not found"}`}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = w.Write([]byte(resp.body))
}

const repoJSON = `{
  "full_name":"octo/agents","owner":"octo","name":"agents","html_url":"https://github.com/octo/agents",
  "default_branch":"main","indexed_commit":"abc","indexed_at":"2026-10-01T00:00:00Z",
  "refreshed_at":"2026-10-02T00:00:00Z","license":{"policy":"licensed","reason":"MIT","spdx_id":"MIT"},
  "agent_count":2,"harnesses":["claude","omp"],"github_stats":{"stars":3,"contributors_too_large":false},
  "fork":false,"manifest":false,"agnthub_stars":1,"problems":[],
  "adoption":{"total":5,"direct":5,"dependency":0,"last_7_days":1,"last_30_days":2,"harnesses":[]},
  "agents":[
    {"id":"reviewer","description":"Reviews code","from_manifest":false,
     "portable":{"path":"agents/reviewer.md","source_url":"u","body":"Review carefully."},
     "variants":[],"dependencies":{"agents":[],"skills":[]},"problems":[],
     "targets":[{"harness":"claude","status":"generated","notes":[]},
                {"harness":"gemini","status":"incompatible","reason":"uses tools","notes":[]}],
     "adoption":{"total":5,"direct":5,"dependency":0,"last_7_days":1,"last_30_days":2,"harnesses":[]},
     "agnthub_stars":1},
    {"id":"planner","from_manifest":false,"variants":[],"dependencies":{"agents":[],"skills":[]},
     "problems":[],"targets":[],"agnthub_stars":0,
     "adoption":{"total":0,"direct":0,"dependency":0,"last_7_days":0,"last_30_days":0,"harnesses":[]}}
  ]}`

func connect(t *testing.T, api *fakeAPI) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	server := mcpserver.New(
		&mcpserver.Client{BaseURL: srv.URL, HTTP: srv.Client()},
		mcpserver.Options{Version: "test"},
	)
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(
	t *testing.T,
	cs *mcp.ClientSession,
	name string,
	args map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", text(res))
	}
	raw, err := sonic.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := sonic.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func repoAPI() *fakeAPI {
	return &fakeAPI{responses: map[string]response{
		"/v1/repositories/github/octo/agents": {http.StatusOK, repoJSON},
	}}
}

func TestListsReadOnlyTools(t *testing.T) {
	cs := connect(t, &fakeAPI{})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be annotated read-only", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"get_agent", "get_repository", "install_command", "search_agents"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestSearchForwardsFiltersWithBoundedPageSize(t *testing.T) {
	api := &fakeAPI{responses: map[string]response{
		"/v1/search": {http.StatusOK, `{"items":[{"agent":"reviewer","repository":{"owner":"octo",
"name":"agents","full_name":"octo/agents"},"harnesses":[{"harness":"claude","status":"native"}],
"portable":true,"license":{"policy":"licensed","reason":"MIT"},"indexed_commit":"abc",
"indexed_at":"2026-10-01T00:00:00Z","refreshed_at":"2026-10-01T00:00:00Z","agnthub_stars":4,
"adoption":{"total":1,"direct":1,"dependency":0,"last_7_days":0,"last_30_days":0,"harnesses":[]}}],
"page":1,"per_page":10,"total":1,"has_next":false,"sort":"stars"}`},
	}}
	cs := connect(t, api)
	page := decode[mcpserver.SearchPage](t, call(t, cs, "search_agents", map[string]any{
		"query": "review", "harnesses": []string{"claude", "omp"}, "sort": "stars",
	}))
	if len(page.Items) != 1 || page.Items[0].AgnthubStars != 4 ||
		page.Items[0].Repository.FullName != "octo/agents" {
		t.Fatalf("unexpected page: %+v", page)
	}
	for _, want := range []string{
		"q=review", "harness=claude", "harness=omp", "sort=stars",
		"per_page=10",
	} {
		if query := *api.lastQuery.Load(); !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}

	res := call(t, cs, "search_agents", map[string]any{"per_page": 50})
	if !res.IsError {
		t.Fatal("per_page above the bound must be rejected")
	}
	res = call(t, cs, "search_agents", map[string]any{"sort": "harness_installs"})
	if !res.IsError {
		t.Fatal("harness_installs without exactly one harness must be rejected")
	}
	res = call(t, cs, "search_agents", map[string]any{"harnesses": []string{"emacs"}})
	if !res.IsError {
		t.Fatal("unknown harness must be rejected")
	}
}

func TestSearchReportsUnavailableEngine(t *testing.T) {
	cs := connect(t, &fakeAPI{responses: map[string]response{
		"/v1/search": {
			http.StatusServiceUnavailable,
			`{"detail":"Search is temporarily unavailable."}`,
		},
	}})
	res := call(t, cs, "search_agents", map[string]any{"query": "x"})
	if !res.IsError || !strings.Contains(text(res), "temporarily unavailable") {
		t.Fatalf("want an unavailable tool error, got %+v", text(res))
	}
}

func TestGetAgentOmitsInstructionsUnlessRequestedAndBoundsThem(t *testing.T) {
	long := strings.Repeat("é", mcpserver.MaxBody)
	api := &fakeAPI{responses: map[string]response{
		"/v1/repositories/github/octo/agents/agents/reviewer": {http.StatusOK, `{
"repository":{"full_name":"octo/agents","owner":"octo","name":"agents","indexed_commit":"abc",
"license":{"policy":"licensed","reason":"MIT"},"github_stats":{"contributors_too_large":false}},
"agent":{"id":"reviewer","from_manifest":false,"portable":{"path":"a.md","source_url":"u",
"body":"` + long + `"},"variants":[],"dependencies":{"agents":[{"agent":"planner"}],"skills":[]},
"problems":[],"targets":[{"harness":"claude","status":"native","notes":[]}],"agnthub_stars":2,
"adoption":{"total":0,"direct":0,"dependency":0,"last_7_days":0,"last_30_days":0,"harnesses":[]}}}`},
	}}
	cs := connect(t, api)
	args := map[string]any{"repository": "octo/agents", "agent": "reviewer"}
	detail := decode[mcpserver.AgentDetail](t, call(t, cs, "get_agent", args))
	if detail.Agent.Portable == nil || detail.Agent.Portable.Body != nil {
		t.Fatalf("instructions must be omitted by default: %+v", detail.Agent.Portable)
	}
	if len(detail.Agent.Dependencies.Agents) != 1 || detail.Agent.Targets[0].Status != "native" {
		t.Fatalf("dependencies and targets must be kept: %+v", detail.Agent)
	}
	args["include_instructions"] = true
	detail = decode[mcpserver.AgentDetail](t, call(t, cs, "get_agent", args))
	body := detail.Agent.Portable.Body
	if body == nil || len(*body) > mcpserver.MaxBody || !detail.Agent.Portable.BodyTruncated ||
		!strings.HasPrefix(long, *body) {
		t.Fatalf("requested instructions must be capped at a rune boundary")
	}

	res := call(t, cs, "get_agent", map[string]any{"repository": "octo/agents", "agent": "missing"})
	if !res.IsError || !strings.Contains(text(res), "not found") {
		t.Fatalf("missing agent must be a not-found tool error: %s", text(res))
	}
	res = call(t, cs, "get_agent", map[string]any{"repository": "../etc", "agent": "reviewer"})
	if !res.IsError {
		t.Fatal("malformed repository must be rejected before any request")
	}
}

func TestGetRepositoryListsAgentsWithoutInstructions(t *testing.T) {
	cs := connect(t, repoAPI())
	repo := decode[mcpserver.Repository](t, call(t, cs, "get_repository",
		map[string]any{"repository": "octo/agents"}))
	if repo.FullName != "octo/agents" || len(repo.Agents) != 2 || repo.Adoption.Total != 5 {
		t.Fatalf("unexpected repository: %+v", repo)
	}
	if repo.Agents[0].Portable == nil || repo.Agents[0].Portable.Body != nil {
		t.Fatal("get_repository must never include instructions")
	}

	res := call(t, cs, "get_repository", map[string]any{"repository": "octo/gone"})
	if !res.IsError || !strings.Contains(text(res), "not found") {
		t.Fatalf("unlisted repository must be a not-found tool error: %s", text(res))
	}
}

func TestInstallCommandValidatesAgainstTheListing(t *testing.T) {
	cs := connect(t, repoAPI())
	out := decode[mcpserver.CommandOutput](t, call(t, cs, "install_command", map[string]any{
		"repository": "octo/agents", "agents": []string{"reviewer"},
		"harnesses": []string{"omp", "gemini", "claude"}, "scope": "global", "omp_profile": "work",
	}))
	want := "npx agnthub add octo/agents --agent reviewer --harness claude --harness gemini " +
		"--harness omp --global --extra omp.profile=work"
	if out.Command != want {
		t.Fatalf("command = %q, want %q", out.Command, want)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "gemini") {
		t.Fatalf("want one warning for the incompatible gemini target: %v", out.Warnings)
	}

	for name, args := range map[string]map[string]any{
		"unknown agent": {"repository": "octo/agents", "agents": []string{"ghost"}},
		"bad profile": {
			"repository": "octo/agents", "harnesses": []string{"omp"}, "omp_profile": "$(id)",
		},
		"all with agents": {"repository": "octo/agents", "all": true, "agents": []string{"reviewer"}},
		"unlisted":        {"repository": "octo/gone"},
		"bad scope":       {"repository": "octo/agents", "scope": "system"},
	} {
		if res := call(t, cs, "install_command", args); !res.IsError {
			t.Errorf("%s: want a tool error, got %s", name, text(res))
		}
	}
}
