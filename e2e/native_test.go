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
	"go.yaml.in/yaml/v3"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// missingAgent is an agent name no fixture defines; Harnesses that list their
// agents when asked for an unknown one are asked for it.
const missingAgent = "agnthub-e2e-no-such-agent"

// nativeContext is what a native check needs: where the Harness runs (the
// project, or a directory outside it for user scope), which agents agnthub
// reports installed where, and the omp profile to run in.
type nativeContext struct {
	scope   scope
	dir     string
	agents  map[string]string
	profile string
}

type nativeCheck func(t *testing.T, n nativeContext)

// nativeChecks ask each real Harness CLI, offline and without signing in, which
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

// nativeContext asks agnthub (agnthub list --json) what the step installed for
// this Harness, so lifecycle steps are checked like installs.
func (s *suite) nativeContext(t *testing.T, st step) nativeContext {
	t.Helper()
	n := nativeContext{
		scope: st.native, dir: projectDir, agents: map[string]string{}, profile: st.ompProfile,
	}
	if st.native == scopeGlobal {
		n.dir = elsewhereDir
	}
	for _, a := range s.installed(t, st.native == scopeGlobal) {
		if st.ompProfile == "" || a.profile == st.ompProfile {
			n.agents[a.agent] = a.path
		}
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

// describedAgents returns the agents a Harness listed whose description is the
// one agnthub installed, so an agent shadowed by a built-in or another file of
// the same name does not count as loaded. Cursor reads frontmatter values
// verbatim, so a description YAML had to quote is listed as written, quotes
// and escapes included, which agnthub accepts as the same description.
func describedAgents(t *testing.T, n nativeContext, listed map[string]string) []string {
	t.Helper()
	var loaded []string
	for id, path := range n.agents {
		description, ok := listed[id]
		if !ok {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := agent.ParseMarkdownLenient(path, raw)
		if err != nil {
			t.Fatalf("installed %s: %v", path, err)
		}
		want := oneLine(doc.String("description"))
		if got := oneLine(description); got == want || unquoted(got) == want {
			loaded = append(loaded, id)
		} else {
			t.Logf("%s: the Harness lists %q, agnthub installed %q", id, description,
				doc.String("description"))
		}
	}
	return loaded
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// unquoted reads a value written as a quoted YAML scalar, escapes included.
func unquoted(s string) string {
	if len(s) >= 2 && s[0] == s[len(s)-1] && (s[0] == '"' || s[0] == '\'') {
		var value string
		if yaml.Unmarshal([]byte(s), &value) == nil {
			return value
		}
		return s[1 : len(s)-1]
	}
	return s
}

// jsonArray decodes the first line-leading JSON array in out into v.
func jsonArray(out []byte, v any) bool {
	for start := 0; start < len(out); {
		i := bytes.IndexByte(out[start:], '[')
		if i < 0 {
			return false
		}
		at := start + i
		if (at == 0 || out[at-1] == '\n') && sonic.Unmarshal(out[at:], v) == nil {
			return true
		}
		start = at + 1
	}
	return false
}

// listedAgents collects name → description from every match of pattern
// (two groups) in text.
func listedAgents(pattern *regexp.Regexp, text string) map[string]string {
	listed := map[string]string{}
	for _, m := range pattern.FindAllStringSubmatch(text, -1) {
		listed[m[1]] = m[2]
	}
	return listed
}

var claudeAgents = regexp.MustCompile(`Available agents: (.*)`)

// checkClaude: Claude Code lists every agent it discovered when asked for an
// unknown one, before any model call. Its debug log must also name, as keys it
// does not recognize, every key conversion omits as one Claude Code ignores.
func checkClaude(t *testing.T, n nativeContext) {
	out := harnessOutput(t, n, nil, "claude", "--debug", "-p", "--agent", missingAgent, "x")
	match := claudeAgents.FindSubmatch(out)
	if match == nil {
		t.Fatalf("claude did not list its agents:\n%s", out)
	}
	requireLoaded(t, n, strings.Split(strings.TrimSpace(string(match[1])), ", "), out)
	requireClaudeIgnores(t, n)
}

// requireClaudeIgnores fails when Claude Code recognizes a frontmatter key of an
// installed agent that conversion omits as ignored, or a hook event conversion
// omits as one Claude Code does not have. Claude Code's debug log names up to
// five unrecognized keys per agent file, so no fixture has more.
func requireClaudeIgnores(t *testing.T, n nativeContext) {
	t.Helper()
	debugLog, err := os.ReadFile(filepath.Join(homeDir, ".claude", "debug", "latest"))
	if err != nil {
		t.Fatalf("claude --debug wrote no log: %v", err)
	}
	for id, path := range n.agents {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := agent.ParseMarkdownLenient(path, raw)
		if err != nil {
			t.Fatalf("installed %s: %v", path, err)
		}
		var line string
		for candidate := range strings.SplitSeq(string(debugLog), "\n") {
			if strings.Contains(candidate, "Agent file "+path+" has ") &&
				strings.Contains(candidate, "does not recognize or apply") {
				line = candidate
			}
		}
		if bytes.Contains(debugLog, []byte("Agent file "+path+" has invalid isolation value")) {
			t.Errorf("%s: Claude Code rejects its isolation value", id)
		}
		for key := range doc.Frontmatter {
			if harness.ClaudeIgnores(key) && !strings.Contains(line, `"`+key+`"`) {
				t.Errorf("%s: Claude Code no longer reports %q as a key it ignores, but "+
					"conversion omits it as one; debug log line: %q", id, key, line)
			}
		}
		events, _ := harness.ClaudeIgnoredHookEvents(doc.Frontmatter["hooks"])
		for _, event := range events {
			want := "Agent '" + id + "': hooks." + event + ": unknown hook event; entry ignored"
			if !bytes.Contains(debugLog, []byte(want)) {
				t.Errorf("%s: Claude Code no longer logs %q, but conversion omits the hook as "+
					"one it ignores", id, want)
			}
		}
	}
}

var codexRole = regexp.MustCompile(`(?:Available roles:|\\n)([A-Za-z0-9_.-]+): \{\\n`)

// checkCodex: `codex debug prompt-input` renders the model input without calling
// a model; custom agents appear as roles of the spawn_agent tool. Project agents
// load only from a trusted project.
func checkCodex(t *testing.T, n nativeContext) {
	codexHome := filepath.Join(homeDir, ".codex")
	writeFile(t, filepath.Join(codexHome, "config.toml"),
		"[projects.\""+projectDir+"\"]\ntrust_level = \"trusted\"\n")
	out := harnessOutput(t, n, []string{"CODEX_HOME=" + codexHome},
		"codex", "debug", "prompt-input", "x")
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
// clean. Each agent must also resolve to the mode its file declares: a
// manual-only agent is written as primary, which keeps it out of the model's
// subagent tool.
func checkOpenCode(t *testing.T, n nativeContext) {
	t.Cleanup(func() { harnessOutput(t, n, nil, "opencode", "service", "stop") })
	var out []byte
	var loaded []string
	modes := map[string]string{}
	for range 30 {
		out = opencodeAgents(t, n)
		var agents []struct {
			ID   string `json:"id"`
			Mode string `json:"mode"`
		}
		loaded = nil
		if jsonArray(out, &agents) {
			for _, a := range agents {
				loaded = append(loaded, a.ID)
				modes[a.ID] = a.Mode
			}
		}
		if allLoaded(n, loaded) {
			break
		}
		<-time.After(time.Second)
	}
	requireLoaded(t, n, loaded, out)
	for id, path := range n.agents {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := agent.ParseMarkdownLenient(path, raw)
		if err != nil {
			t.Fatalf("installed %s: %v", path, err)
		}
		if want := doc.String("mode"); want != "" && modes[id] != "" && modes[id] != want {
			t.Errorf("%s: OpenCode resolved mode %s, the file declares %s", id, modes[id], want)
		}
	}
}

// opencodeAgents runs `opencode debug agents` with stdout in a file: OpenCode
// exits before a pipe drains, which cuts its JSON once it outgrows the pipe
// buffer (about 64 KiB, some 30 agents).
func opencodeAgents(t *testing.T, n nativeContext) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agents.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("opencode", "debug", "agents")
	cmd.Dir = n.dir
	cmd.Env = harnessEnv()
	cmd.Stdout, cmd.Stderr = file, file
	runErr := runTimeout(cmd, 3*time.Minute)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		t.Fatalf("opencode debug agents: %v", runErr)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
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

// geminiSubagent is how Gemini CLI lists available subagents in its system
// instruction: "- <name> -> <description>".
var geminiSubagent = regexp.MustCompile(`(?m)^- ([a-z0-9_-]+) -> (.*)$`)

// checkGemini runs Gemini CLI headless (-p) with an API key against the model
// stub: its first model request lists the subagents it registered. Gemini CLI
// registers a project agent only after the user acknowledged that file (it asks
// on the next interactive start, keyed by the file's SHA-256); the check
// acknowledges exactly the files agnthub installed, as that prompt would.
func checkGemini(t *testing.T, n nativeContext) {
	stub := startModelStub(t)
	geminiDir := filepath.Join(homeDir, ".gemini")
	writeFile(t, filepath.Join(geminiDir, "settings.json"),
		`{"security":{"auth":{"selectedType":"gemini-api-key"}}}`)
	if n.scope == scopeProject {
		acknowledgeGeminiAgents(t, n.agents)
	}
	out := harnessOutput(t, n, []string{
		"GEMINI_API_KEY=agnthub-e2e",
		"GOOGLE_GEMINI_BASE_URL=" + stub.url,
		"GEMINI_CLI_TRUST_WORKSPACE=true",
	}, "gemini", "-m", "gemini-2.5-flash", "-p", "x", "-o", "text")
	for _, failure := range []string{"Agent loading error", "Error registering"} {
		if bytes.Contains(out, []byte(failure)) {
			t.Errorf("Gemini CLI reported %q:\n%s", failure, out)
		}
	}
	var system strings.Builder
	for _, req := range stub.received() {
		if !strings.Contains(req.path, ":streamGenerateContent") {
			continue
		}
		var body struct {
			SystemInstruction struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
		}
		if err := sonic.Unmarshal(req.body, &body); err != nil {
			t.Fatal(err)
		}
		for _, p := range body.SystemInstruction.Parts {
			system.WriteString(p.Text)
		}
		break
	}
	if system.Len() == 0 {
		t.Fatalf("Gemini CLI sent no model request:\n%s", out)
	}
	requireLoaded(t, n, describedAgents(t, n, listedAgents(geminiSubagent, system.String())), out)
}

// ompTaskAgent is how omp's task tool lists the agents it can run:
// "- `<name>` (notes): <description>".
var ompTaskAgent = regexp.MustCompile("(?m)^- `([^`]+)`(?: \\([^)]*\\))?: (.*)$")

// checkOMP runs omp headless (-p) with a models.yml provider pointing at the
// model stub, in the step's omp profile: the task tool of its first model
// request lists every agent it discovered.
func checkOMP(t *testing.T, n nativeContext) {
	stub := startModelStub(t)
	agentDir := filepath.Join(homeDir, ".omp", "agent")
	args := []string{"-p", "--no-session", "--model", "stub/stub-model", "x"}
	if n.profile != "" {
		agentDir = filepath.Join(homeDir, ".omp", "profiles", n.profile, "agent")
		args = append([]string{"--profile", n.profile}, args...)
	}
	writeFile(t, filepath.Join(agentDir, "models.yml"), "providers:\n  stub:\n    baseUrl: "+
		stub.url+"/v1\n    auth: none\n    api: openai-completions\n    models:\n      - id: stub-model\n")
	out := harnessOutput(t, n, nil, "omp", args...)
	description := ""
	for _, req := range stub.received() {
		if description = chatToolDescription(t, req, "task"); description != "" {
			break
		}
	}
	if description == "" {
		t.Fatalf("omp sent no model request with its task tool:\n%s", out)
	}
	requireLoaded(t, n, describedAgents(t, n, listedAgents(ompTaskAgent, description)), out)
}

// cursorSubagent is how Cursor's Task tool lists subagents: "- <name>: <description>".
var cursorSubagent = regexp.MustCompile(`(?m)^- ([A-Za-z0-9_.-]+): (.*)$`)

// checkCursor runs Cursor's agent CLI headless against the model stub. Every
// cursor-agent command needs a signed-in account, so the check runs the same
// release's agent-cli-local build (Cursor Private Inference, installed beside
// cursor-agent in the image), which runs without Cursor auth (--authless)
// against an OpenAI-compatible endpoint and shares cursor-agent's workspace
// subagent loader: its Task tool lists the subagents it loaded.
func checkCursor(t *testing.T, n nativeContext) {
	stub := startModelStub(t)
	out := harnessOutput(t, n, []string{"CURSOR_LOCAL_AGENT_BASE_URL=" + stub.url + "/v1"},
		"cursor-agent-local", "--authless", "-p", "--trust", "--model", "stub-model", "x")
	description := ""
	for _, req := range stub.received() {
		if description = chatToolDescription(t, req, "Task"); description != "" {
			break
		}
	}
	if description == "" {
		t.Fatalf("cursor-agent-local sent no model request with its Task tool:\n%s", out)
	}
	loaded := describedAgents(t, n, listedAgents(cursorSubagent, description))
	if reason, excluded := nativeExclusions["cursor"][string(n.scope)]; excluded {
		if len(loaded) > 0 {
			t.Errorf("Cursor's CLI now loads %s-scope agents %v; remove the nativeExclusions "+
				"entry in coverage.go (%s)", n.scope, loaded, reason)
			return
		}
		t.Logf("as recorded in nativeExclusions, Cursor's CLI loads none of the %d %s-scope agents",
			len(n.agents), n.scope)
		return
	}
	requireLoaded(t, n, loaded, out)
}

// chatToolDescription returns the description of the tool named name in a Chat
// Completions request, or "".
func chatToolDescription(t *testing.T, req stubRequest, name string) string {
	t.Helper()
	if !strings.HasSuffix(req.path, "/chat/completions") {
		return ""
	}
	var body struct {
		Tools []struct {
			Function struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := sonic.Unmarshal(req.body, &body); err != nil {
		t.Fatal(err)
	}
	for _, tool := range body.Tools {
		if tool.Function.Name == name {
			return tool.Function.Description
		}
	}
	return ""
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
