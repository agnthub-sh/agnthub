package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/heynemann/agnthub/internal/cli"
	"github.com/heynemann/agnthub/platform/env"
	"github.com/heynemann/agnthub/state"
)

// mcpSession runs `agnthub mcp` over pipes and connects a real MCP client to it.
func (h *harnessT) mcpSession(apiURL string) *mcp.ClientSession {
	h.t.Helper()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	var stderr bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:      env.FromMap(map[string]string{"HOME": h.home, "AGNTHUB_API_URL": apiURL}),
		WorkDir:  h.project,
		Stdin:    stdinR,
		Stdout:   stdoutW,
		Stderr:   &stderr,
		Now:      func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	done := make(chan int, 1)
	go func() {
		done <- app.Run(context.Background(), []string{"mcp"})
		_ = stdoutW.Close()
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(
		context.Background(),
		&mcp.IOTransport{Reader: stdoutR, Writer: stdinW},
		nil,
	)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		_ = cs.Close()
		if code := <-done; code != 0 {
			h.t.Errorf("agnthub mcp exited %d: %s", code, stderr.String())
		}
	})
	return cs
}

type installResult struct {
	Status     string `json:"status"`
	ProjectDir string `json:"project_dir"`
	Message    string `json:"message"`
	Plan       struct {
		Status string `json:"status"`
		Items  []struct {
			Agent  string `json:"agent"`
			Action string `json:"action"`
			Reason string `json:"reason"`
		} `json:"items"`
	} `json:"plan"`
}

func (h *harnessT) install(cs *mcp.ClientSession, args map[string]any) (installResult, bool) {
	h.t.Helper()
	res, err := cs.CallTool(context.Background(),
		&mcp.CallToolParams{Name: "install_agents", Arguments: args})
	if err != nil {
		h.t.Fatal(err)
	}
	var out installResult
	raw, err := sonic.Marshal(res.StructuredContent)
	if err != nil {
		h.t.Fatal(err)
	}
	if res.StructuredContent != nil {
		if err := sonic.Unmarshal(raw, &out); err != nil {
			h.t.Fatal(err)
		}
	}
	return out, res.IsError
}

func TestMCPServesRegistryAndInstallTools(t *testing.T) {
	h := newHarness(t)
	cs := h.mcpSession("http://127.0.0.1:1")
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	want := []string{
		"get_agent", "get_repository", "install_agents", "install_command",
		"search_agents",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestMCPReadsTheConfiguredAPI(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"page":1,"per_page":10,"total":0,"has_next":false,` +
			`"sort":"recent"}`))
	}))
	defer api.Close()
	h := newHarness(t)
	cs := h.mcpSession(api.URL)
	res, err := cs.CallTool(context.Background(),
		&mcp.CallToolParams{Name: "search_agents", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("search against AGNTHUB_API_URL failed: %v %+v", err, res)
	}
}

func TestMCPInstallPreviewsUntilConfirmed(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	cs := h.mcpSession("http://127.0.0.1:1")
	args := map[string]any{
		"source": h.source, "agents": []string{"reviewer"}, "harnesses": []string{"claude"},
	}

	out, isErr := h.install(cs, args)
	if isErr || out.Status != "preview" || out.Plan.Status != "planned" ||
		len(out.Plan.Items) != 1 || out.Plan.Items[0].Action != "create" ||
		out.ProjectDir != h.project {
		t.Fatalf("want a preview of one create: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(h.project, ".claude")); !os.IsNotExist(err) {
		t.Fatal("a preview must not write anything")
	}

	args["confirm"] = true
	out, isErr = h.install(cs, args)
	if isErr || out.Status != "applied" {
		t.Fatalf("confirmed install must apply: %+v", out)
	}
	if !strings.Contains(h.installed(".claude/agents/reviewer.md"), "Review the diff carefully.") {
		t.Fatal("agent was not installed")
	}
	if _, err := state.LoadLock(filepath.Join(h.project, state.LockFileName)); err != nil {
		t.Fatalf("install must record the lock: %v", err)
	}
}

func TestMCPInstallNeverOverwritesUnmanagedFiles(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	target := filepath.Join(h.project, ".claude", "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs := h.mcpSession("http://127.0.0.1:1")
	out, isErr := h.install(cs, map[string]any{
		"source": h.source, "agents": []string{"reviewer"}, "harnesses": []string{"claude"},
		"confirm": true,
	})
	if !isErr || out.Status != "refused" || len(out.Plan.Items) != 1 ||
		!strings.Contains(out.Plan.Items[0].Reason, "not managed by agnthub") {
		t.Fatalf("an unmanaged file must refuse the plan: %+v", out)
	}
	if h.installed(".claude/agents/reviewer.md") != "mine" {
		t.Fatal("unmanaged file was overwritten")
	}
}

func TestMCPInstallHonorsScopeSettingsAndValidation(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	other := t.TempDir()
	cs := h.mcpSession("http://127.0.0.1:1")

	out, isErr := h.install(cs, map[string]any{
		"source": h.source, "agents": []string{"*"}, "harnesses": []string{"claude"},
		"project_dir": other, "confirm": true,
	})
	if isErr || out.Status != "applied" || out.ProjectDir != other {
		t.Fatalf("project_dir must select the project: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(other, ".claude", "agents", "reviewer.md")); err != nil {
		t.Fatalf("agent not installed into project_dir: %v", err)
	}

	for name, args := range map[string]map[string]any{
		"relative project_dir": {
			"source": h.source, "agents": []string{"reviewer"}, "harnesses": []string{"claude"},
			"project_dir": "rel",
		},
		"extra for an unselected harness": {
			"source": h.source, "agents": []string{"reviewer"}, "harnesses": []string{"claude"},
			"extra": []string{"omp.profile=work"},
		},
		"unknown agent": {
			"source": h.source, "agents": []string{"ghost"}, "harnesses": []string{"claude"},
		},
		"no harness": {"source": h.source, "agents": []string{"reviewer"}, "harnesses": []string{}},
	} {
		if _, isErr := h.install(cs, args); !isErr {
			t.Errorf("%s: want a tool error", name)
		}
	}
	if _, err := os.Stat(filepath.Join(h.project, ".claude")); !os.IsNotExist(err) {
		t.Fatal("rejected calls must not write anything")
	}
}
