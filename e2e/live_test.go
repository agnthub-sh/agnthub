//go:build e2e && linux

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

// liveToken is in the live-probe agent's instructions only (never in a
// prompt), so a reply containing it proves the Harness ran the installed agent.
const liveToken = "AGNTHUB-LIVE-7Q4F"

// liveDelegate is the prompt for Harnesses that run a custom agent only as a
// subagent of their main agent.
const liveDelegate = "Use your subagent tool to run the live-probe agent exactly once with " +
	"the task 'say hello'. Then reply with exactly the text it returned and nothing else."

// liveAttempts bounds retries: a delegating main agent run by a small model
// occasionally answers itself instead of calling its subagent tool.
const liveAttempts = 3

// liveModels are the default models; E2E_LIVE_MODEL overrides them.
var liveModels = map[string]string{
	"claude":   "anthropic/claude-haiku-4.5",
	"opencode": "anthropic/claude-haiku-4.5",
	"codex":    "openai/gpt-5-mini",
	"copilot":  "anthropic/claude-haiku-4.5",
	"cursor":   "",
	"gemini":   "gemini-2.5-flash",
	"omp":      "anthropic/claude-haiku-4.5",
}

// liveRun is one Harness invocation of the installed live-probe agent.
type liveRun struct {
	env  []string
	name string
	args []string
}

// TestLive installs the live fixture into the Harness (E2E_HARNESS) and runs the
// installed agent with a real, cheap model, asserting its fixed reply. It runs
// with make e2e-live (network on, E2E_LIVE=1) and skips when the Harness's
// secret (liveCredentials in coverage.go) is not set.
func TestLive(t *testing.T) {
	harness := processEnv.Get("E2E_HARNESS")
	if harness == "" || processEnv.Get("E2E_LIVE") != "1" {
		t.Skip("the live tier runs in its container with make e2e-live")
	}
	cred := liveCredentials[harness]
	secret := ""
	for _, name := range cred.secrets {
		if secret = processEnv.Get(name); secret != "" {
			break
		}
	}
	if secret == "" {
		t.Skipf("%s is not set: the live tier cannot run %s", strings.Join(cred.secrets, " or "),
			harness)
	}
	model := liveModels[harness]
	if m := processEnv.Get("E2E_LIVE_MODEL"); m != "" {
		model = m
	}
	if err := os.RemoveAll(runRoot); err != nil {
		t.Fatal(err)
	}
	mkdirAll(t, scratchDir)
	resetDirs(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "fixtures", "live"))
	if err != nil {
		t.Fatal(err)
	}
	s := &suite{harness: harness}
	exit, stdout, stderr := s.agnthub(t, nil,
		argv("add", fixture, "--agent", "live-probe", s.harnessFlag(), "--yes"))
	if exit != 0 {
		t.Fatalf("agnthub add: exit status %d\n%s%s", exit, stdout, stderr)
	}
	run := liveInvocation(t, harness, secret, model)
	n := nativeContext{scope: scopeProject, dir: projectDir}
	for attempt := 1; attempt <= liveAttempts; attempt++ {
		out := string(harnessOutput(t, n, run.env, run.name, run.args...))
		if strings.Contains(out, liveToken) {
			t.Logf("%s ran the installed live-probe agent (attempt %d, model %q)", harness, attempt,
				model)
			return
		}
		t.Logf("attempt %d: the reply does not contain the agent's token:\n%s", attempt,
			strings.ReplaceAll(out, secret, "<secret>"))
	}
	t.Errorf("%s never ran the installed live-probe agent", harness)
}

// liveInvocation configures the Harness for a real model and returns how to run
// the installed agent.
func liveInvocation(t *testing.T, harness, secret, model string) liveRun {
	t.Helper()
	switch harness {
	case "claude":
		return liveRun{
			env: []string{
				"ANTHROPIC_BASE_URL=https://openrouter.ai/api",
				"ANTHROPIC_AUTH_TOKEN=" + secret,
				"ANTHROPIC_API_KEY=",
				"ANTHROPIC_MODEL=" + model,
				"ANTHROPIC_SMALL_FAST_MODEL=" + model,
				"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model,
				"ANTHROPIC_DEFAULT_SONNET_MODEL=" + model,
				"ANTHROPIC_DEFAULT_OPUS_MODEL=" + model,
				"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
			},
			name: "claude", args: []string{"-p", "--agent", "live-probe", "Say hello."},
		}
	case "opencode":
		t.Cleanup(func() {
			harnessOutput(t, nativeContext{dir: projectDir}, nil, "opencode", "service", "stop")
		})
		return liveRun{
			env:  []string{"OPENROUTER_API_KEY=" + secret},
			name: "opencode",
			args: []string{
				"run",
				"--agent",
				"live-probe",
				"-m",
				"openrouter/" + model,
				"Say hello.",
			},
		}
	case "codex":
		codexHome := filepath.Join(homeDir, ".codex")
		writeFile(t, filepath.Join(codexHome, "config.toml"), "model = \""+model+"\"\n"+
			"model_provider = \"openrouter\"\n\n[model_providers.openrouter]\n"+
			"name = \"OpenRouter\"\nbase_url = \"https://openrouter.ai/api/v1\"\n"+
			"env_key = \"OPENROUTER_API_KEY\"\nwire_api = \"responses\"\n\n"+
			"[projects.\""+projectDir+"\"]\ntrust_level = \"trusted\"\n")
		return liveRun{
			env:  []string{"CODEX_HOME=" + codexHome, "OPENROUTER_API_KEY=" + secret},
			name: "codex",
			args: []string{"exec", "--json", "--skip-git-repo-check", liveDelegate},
		}
	case "copilot":
		return liveRun{
			env: []string{
				"COPILOT_PROVIDER_BASE_URL=https://openrouter.ai/api/v1",
				"COPILOT_PROVIDER_API_KEY=" + secret,
				"COPILOT_MODEL=" + model,
			},
			name: "copilot", args: []string{"-p", "Say hello.", "--agent", "live-probe"},
		}
	case "cursor":
		args := []string{"-p", "--trust", "--output-format", "stream-json"}
		if model != "" {
			args = append(args, "--model", model)
		}
		return liveRun{
			env:  []string{"CURSOR_API_KEY=" + secret},
			name: "cursor-agent", args: append(args, liveDelegate),
		}
	case "gemini":
		geminiDir := filepath.Join(homeDir, ".gemini")
		writeFile(t, filepath.Join(geminiDir, "settings.json"),
			`{"security":{"auth":{"selectedType":"gemini-api-key"}}}`)
		acknowledgeGeminiAgents(t, map[string]string{
			"live-probe": filepath.Join(projectDir, ".gemini", "agents", "live-probe.md"),
		})
		return liveRun{
			env:  []string{"GEMINI_API_KEY=" + secret, "GEMINI_CLI_TRUST_WORKSPACE=true"},
			name: "gemini",
			args: []string{"-m", model, "--yolo", "-o", "stream-json", "-p", liveDelegate},
		}
	case "omp":
		return liveRun{
			env:  []string{"OPENROUTER_API_KEY=" + secret},
			name: "omp",
			args: []string{"-p", "--no-session", "--model", "openrouter/" + model, liveDelegate},
		}
	}
	t.Fatalf("no live invocation for %s", harness)
	return liveRun{}
}

// acknowledgeGeminiAgents records the project agents as acknowledged, as Gemini
// CLI's interactive prompt does: it registers a project agent only when the
// SHA-256 of the file's content was acknowledged for the project.
func acknowledgeGeminiAgents(t *testing.T, agents map[string]string) {
	t.Helper()
	acknowledged := map[string]string{}
	for id, path := range agents {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		acknowledged[id] = hex.EncodeToString(sum[:])
	}
	ack, err := sonic.MarshalString(map[string]any{projectDir: acknowledged})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(homeDir, ".gemini", "acknowledgments", "agents.json"), ack)
}
