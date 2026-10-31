//go:build e2e && linux

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

// missingAgent is an agent name no fixture defines; Harnesses that list their
// agents when asked for an unknown one are asked for it.
const missingAgent = "agnthub-e2e-no-such-agent"

// nativeContext is what a native check needs: where the Harness runs (the
// project, or a directory outside it for user scope) and which agents agnthub
// installed where.
type nativeContext struct {
	scope  scope
	dir    string
	agents map[string]string
}

type nativeCheck func(t *testing.T, n nativeContext)

// nativeChecks ask each real Harness, offline and without signing in, which
// agents it loads.
var nativeChecks = map[string]nativeCheck{
	"claude":   checkClaude,
	"opencode": checkOpenCode,
	"codex":    checkCodex,
	"copilot":  checkCopilot,
	"cursor":   checkCursor,
	"gemini":   checkGemini,
	"omp":      checkOMP,
}

func (s *suite) nativeContext(sc scope, res *result) nativeContext {
	n := nativeContext{scope: sc, dir: projectDir, agents: res.plan.installed(s.harness)}
	if sc == scopeGlobal {
		n.dir = elsewhereDir
	}
	return n
}

// harnessEnv is a minimal environment: the temporary home and nothing of the
// user's configuration.
func harnessEnv(extra ...string) []string {
	return append([]string{
		"PATH=" + processEnv.Get("PATH"),
		"HOME=" + homeDir,
		"LANG=C.UTF-8",
		"TERM=dumb",
		"NO_COLOR=1",
	}, extra...)
}

// harnessOutput runs a Harness command in n.dir and returns its combined output;
// the exit status is the caller's to judge (listing an unknown agent fails).
func harnessOutput(
	t *testing.T,
	n nativeContext,
	extraEnv []string,
	name string,
	args ...string,
) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = n.dir
	cmd.Env = harnessEnv(extraEnv...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := runTimeout(cmd, 3*time.Minute); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out.Bytes())
		}
	}
	return out.Bytes()
}

// requireLoaded fails for every installed agent the Harness did not load.
func requireLoaded(t *testing.T, n nativeContext, loaded []string, output []byte) {
	t.Helper()
	if len(n.agents) == 0 {
		t.Fatal("the step installed no agents for this Harness")
	}
	var missing []string
	for id := range n.agents {
		if !slices.Contains(loaded, id) {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("the Harness did not load %v; it loaded %v\n%s", missing, loaded, output)
		return
	}
	t.Logf("loaded all %d installed agents (%s scope)", len(n.agents), n.scope)
}

var claudeAgents = regexp.MustCompile(`Available agents: (.*)`)

// checkClaude: Claude Code lists every agent it discovered when asked for an
// unknown one, before any model call.
func checkClaude(t *testing.T, n nativeContext) {
	out := harnessOutput(t, n, nil, "claude", "-p", "--agent", missingAgent, "x")
	match := claudeAgents.FindSubmatch(out)
	if match == nil {
		t.Fatalf("claude did not list its agents:\n%s", out)
	}
	requireLoaded(t, n, strings.Split(strings.TrimSpace(string(match[1])), ", "), out)
}

var codexRole = regexp.MustCompile(`(?:Available roles:|\\n)([A-Za-z0-9_.-]+): \{\\n`)

// checkCodex: `codex debug prompt-input` renders the model input without calling
// a model; custom agents appear as roles of the spawn_agent tool. Project agents
// load only from a trusted project.
func checkCodex(t *testing.T, n nativeContext) {
	codexHome := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	trust := "[projects.\"" + projectDir + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(
		filepath.Join(codexHome, "config.toml"),
		[]byte(trust),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	out := harnessOutput(
		t,
		n,
		[]string{"CODEX_HOME=" + codexHome},
		"codex",
		"debug",
		"prompt-input",
		"x",
	)
	_, roles, found := bytes.Cut(out, []byte("Available roles:"))
	if !found {
		t.Fatalf("codex rendered no custom agent roles:\n%s", out)
	}
	var loaded []string
	for _, match := range codexRole.FindAllSubmatch(append([]byte("Available roles:"), roles...), -1) {
		loaded = append(loaded, string(match[1]))
	}
	requireLoaded(t, n, loaded, nil)
}

// checkOpenCode: `opencode debug agents` asks OpenCode's background service for
// its resolved agents. The service loads them lazily, so the first answers can
// be incomplete; the service is stopped afterwards so the next step starts
// clean.
func checkOpenCode(t *testing.T, n nativeContext) {
	t.Cleanup(func() { harnessOutput(t, n, nil, "opencode", "service", "stop") })
	var out []byte
	var loaded []string
	for range 30 {
		out = harnessOutput(t, n, nil, "opencode", "debug", "agents")
		var agents []struct {
			ID string `json:"id"`
		}
		loaded = nil
		if start := bytes.IndexByte(
			out,
			'[',
		); start >= 0 &&
			sonic.Unmarshal(out[start:], &agents) == nil {
			for _, agent := range agents {
				loaded = append(loaded, agent.ID)
			}
		}
		if allLoaded(n, loaded) {
			break
		}
		<-time.After(time.Second)
	}
	requireLoaded(t, n, loaded, out)
}

func allLoaded(n nativeContext, loaded []string) bool {
	for id := range n.agents {
		if !slices.Contains(loaded, id) {
			return false
		}
	}
	return true
}

var copilotAgents = regexp.MustCompile(`available: (.*)`)

// checkCopilot: Copilot CLI names its available agents when asked for an unknown
// one. Without a GitHub login it needs a (never contacted) BYOK provider and
// model to get that far.
func checkCopilot(t *testing.T, n nativeContext) {
	out := harnessOutput(t, n, []string{
		"COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:9/v1",
		"COPILOT_MODEL=agnthub-e2e",
	}, "copilot", "-p", "x", "--agent", missingAgent)
	match := copilotAgents.FindSubmatch(out)
	if match == nil {
		t.Fatalf("copilot did not list its agents:\n%s", out)
	}
	requireLoaded(t, n, strings.Split(strings.TrimSpace(string(match[1])), ", "), out)
}

// checkGemini runs Gemini CLI's own agent loader and storage paths (see
// testdata/checks/gemini.mjs): the installed agents must load from the directory
// Gemini reads for that scope, with no loading error.
func checkGemini(t *testing.T, n nativeContext) {
	out := harnessOutput(t, n, nil, "node", checkScript(t, "gemini.mjs"))
	var report map[string]struct {
		Dir    string `json:"dir"`
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
		Errors []string `json:"errors"`
	}
	if err := sonic.Unmarshal(lastLine(out), &report); err != nil {
		t.Fatalf("gemini loader: %v\n%s", err, out)
	}
	tier := report["project"]
	if n.scope == scopeGlobal {
		tier = report["user"]
	}
	for _, path := range n.agents {
		if filepath.Dir(path) != tier.Dir {
			t.Errorf(
				"agnthub installed %s, but Gemini CLI reads %s agents from %s",
				path,
				n.scope,
				tier.Dir,
			)
		}
	}
	if len(tier.Errors) > 0 {
		t.Errorf("Gemini CLI reported loading errors: %v", tier.Errors)
	}
	loaded := make([]string, 0, len(tier.Agents))
	for _, agent := range tier.Agents {
		loaded = append(loaded, agent.Name)
	}
	requireLoaded(t, n, loaded, out)
}

// checkOMP runs omp's own agent discovery (see testdata/checks/omp.ts): each
// installed agent must be found at the file agnthub wrote, not shadowed.
func checkOMP(t *testing.T, n nativeContext) {
	cli, err := exec.LookPath("omp")
	if err != nil {
		t.Fatal(err)
	}
	cli, err = filepath.EvalSymlinks(cli)
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Dir(filepath.Dir(cli)) // <package>/dist/cli.js
	out := harnessOutput(t, n, nil, "bun", checkScript(t, "omp.ts"), pkg)
	var report struct {
		Agents []struct {
			Name     string `json:"name"`
			FilePath string `json:"filePath"`
		} `json:"agents"`
	}
	if err := sonic.Unmarshal(lastLine(out), &report); err != nil {
		t.Fatalf("omp discovery: %v\n%s", err, out)
	}
	var loaded []string
	for _, agent := range report.Agents {
		if n.agents[agent.Name] == agent.FilePath {
			loaded = append(loaded, agent.Name)
		}
	}
	requireLoaded(t, n, loaded, out)
}

// checkCursor runs cursor-agent's own workspace subagent loader sliced out of its
// bundle (see testdata/checks/cursor.cjs); every cursor-agent command needs a
// signed-in account.
func checkCursor(t *testing.T, n nativeContext) {
	if n.scope == scopeGlobal {
		// Recorded in docs/research/harness-agent-formats.md: agnthub installs user
		// agents where Cursor documents them (~/.cursor/agents), but cursor-agent
		// only loads the workspace's .cursor, .claude and .grok agent folders.
		t.Skip("cursor-agent loads no user-scope agents; ~/.cursor/agents is the editor's location")
	}
	cli, err := exec.LookPath("cursor-agent")
	if err != nil {
		t.Fatal(err)
	}
	cli, err = filepath.EvalSymlinks(cli)
	if err != nil {
		t.Fatal(err)
	}
	out := harnessOutput(t, n, nil, "node", checkScript(t, "cursor.cjs"), filepath.Dir(cli), n.dir)
	var report struct {
		Agents []struct {
			Name     string `json:"name"`
			FullPath string `json:"fullPath"`
		} `json:"agents"`
	}
	if err := sonic.Unmarshal(lastLine(out), &report); err != nil {
		t.Fatalf("cursor loader: %v\n%s", err, out)
	}
	var loaded []string
	for _, agent := range report.Agents {
		if n.agents[agent.Name] == agent.FullPath {
			loaded = append(loaded, agent.Name)
		}
	}
	requireLoaded(t, n, loaded, out)
}

func checkScript(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "checks", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func lastLine(out []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	return lines[len(lines)-1]
}
