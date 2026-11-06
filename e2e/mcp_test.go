//go:build e2e && linux

package e2e

import (
	"bytes"
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

// The MCP setup steps prove, per Harness, that `agnthub mcp install` writes an
// entry the real Harness CLI lists (offline, without a model or a sign-in),
// replacing the removed local server and keeping a neighbor server, and that
// `agnthub mcp uninstall` removes only it. See e2e/COVERAGE.md.

const (
	mcpURL      = "https://mcp.agnthub.sh"
	neighborURL = "https://neighbor.example.invalid/mcp"
)

// mcpServers are the MCP servers a Harness lists: name → URL ("" when the
// listing shows no URL).
type mcpServers map[string]string

// mcpHarness is how one Harness keeps and lists MCP servers.
type mcpHarness struct {
	// file is the user configuration under the home directory (omp: of the
	// default profile); seed is its starting content with a neighbor server and
	// the removed local agnthub server.
	file, seed string
	// mechanism is how agnthub is expected to change it.
	mechanism string
	list      func(t *testing.T, profile string) mcpServers
}

const stdioLegacy = `{"command": "npx", "args": ["-y", "agnthub-cli", "mcp"]}`

var mcpHarnesses = map[string]mcpHarness{
	"claude": {
		file: ".claude.json",
		seed: `{"mcpServers": {"other": {"type": "http", "url": "` + neighborURL + `"}, "agnthub": ` +
			`{"type": "stdio", "command": "npx", "args": ["-y", "agnthub-cli", "mcp"], "env": {}}}}`,
		mechanism: "command",
		list:      listClaude,
	},
	"opencode": {
		file: ".config/opencode/opencode.json",
		seed: `{"mcp": {"servers": {"other": {"type": "remote", "url": "` + neighborURL + `"}, ` +
			`"agnthub": {"type": "local", "command": ["npx", "-y", "agnthub-cli", "mcp"]}}}}`,
		mechanism: "file",
		list:      listOpenCode,
	},
	"codex": {
		file: ".codex/config.toml",
		seed: "# neighbor\n[mcp_servers.other]\nurl = \"" + neighborURL + "\" # keep\n\n" +
			"[mcp_servers.agnthub]\ncommand = \"npx\"\nargs = [\"-y\", \"agnthub-cli\", \"mcp\"]\n",
		mechanism: "file",
		list:      listCodex,
	},
	"copilot": {
		file: ".copilot/mcp-config.json",
		seed: `{"mcpServers": {"other": {"type": "http", "url": "` + neighborURL + `", "tools": ["*"]}, ` +
			`"agnthub": {"type": "local", "command": "npx", "args": ["-y", "agnthub-cli", "mcp"], "tools": ["*"]}}}`,
		mechanism: "command",
		list:      listCopilot,
	},
	"cursor": {
		file:      ".cursor/mcp.json",
		seed:      `{"mcpServers": {"other": {"url": "` + neighborURL + `"}, "agnthub": ` + stdioLegacy + `}}`,
		mechanism: "file",
		list:      listCursor,
	},
	"gemini": {
		file: ".gemini/settings.json",
		seed: `{"mcpServers": {"other": {"url": "` + neighborURL + `", "type": "http"}, "agnthub": ` +
			stdioLegacy + `}}`,
		mechanism: "command",
		list:      listGemini,
	},
	"omp": {
		file:      ".omp/agent/mcp.json",
		seed:      `{"mcpServers": {"other": {"type": "http", "url": "` + neighborURL + `"}, "agnthub": ` + stdioLegacy + `}}`,
		mechanism: "file",
		list:      listOMP,
	},
}

// mcpResult is the part of `agnthub mcp … --json` the suite reads.
type mcpResult struct {
	Status  string `json:"status"`
	Results []struct {
		Harness   string `json:"harness"`
		Profile   string `json:"profile"`
		Action    string `json:"action"`
		Mechanism string `json:"mechanism"`
	} `json:"results"`
}

func (s *suite) mcp(t *testing.T) {
	t.Run("mcp", func(t *testing.T) {
		h := mcpHarnesses[s.harness]
		resetDirs(t)
		writeFile(t, filepath.Join(homeDir, h.file), h.seed)
		profiles := []string{""}
		if s.harness == "omp" {
			// A named profile with only the neighbor server.
			writeFile(t, filepath.Join(homeDir, ".omp", "profiles", "work", "agent", "mcp.json"),
				`{"mcpServers": {"other": {"type": "http", "url": "`+neighborURL+`"}}}`)
			profiles = append(profiles, "work")
		}
		install := []string{"mcp", "install", "--harness", s.harness, "--yes", "--json"}

		t.Run("bare-mcp-notice", func(t *testing.T) {
			exit, _, stderr := s.agnthub(t, nil, []string{"mcp"})
			if exit != 1 || !bytes.Contains(stderr, []byte("removed in v0.5.0")) ||
				!bytes.Contains(stderr, []byte("mcp install")) {
				t.Fatalf("exit %d\n%s", exit, stderr)
			}
			exit, _, stderr = s.agnthub(t, nil, []string{"mcp", "install"})
			if exit != 1 || !bytes.Contains(stderr, []byte("--harness")) {
				t.Fatalf("install without --harness and a terminal: exit %d\n%s", exit, stderr)
			}
		})
		t.Run("install-replaces-local-server", func(t *testing.T) {
			want := "update"
			if s.harness == "omp" {
				want = "update install" // default profile seeded, work profile empty
			}
			s.mcpStep(t, install, want, h.mechanism)
			for _, profile := range profiles {
				requireServers(t, h.list(t, profile), true)
			}
			s.mcpStep(t, install, strings.Repeat("unchanged ", len(profiles)), "")
		})
		t.Run("uninstall", func(t *testing.T) {
			s.mcpStep(
				t,
				[]string{"mcp", "uninstall", "--yes", "--json"},
				strings.Repeat("remove ", len(profiles)),
				h.mechanism,
			)
			for _, profile := range profiles {
				requireServers(t, h.list(t, profile), false)
			}
		})
		t.Run("install-again-and-api-key", func(t *testing.T) {
			s.mcpStep(t, install, strings.Repeat("install ", len(profiles)), h.mechanism)
			for _, profile := range profiles {
				requireServers(t, h.list(t, profile), true)
			}
			exit, stdout, stderr := s.agnthub(t, []string{"AGNTHUB_API_KEY=agh_e2etestkey"},
				append(slices.Clone(install), "--api-key"))
			if exit != 0 || !bytes.Contains(stderr, []byte("plain text")) {
				t.Fatalf("--api-key: exit %d\n%s%s", exit, stdout, stderr)
			}
			for _, profile := range profiles {
				requireServers(t, h.list(t, profile), true)
			}
			s.mcpStep(t, []string{"mcp", "uninstall", "--harness", s.harness, "--yes", "--json"},
				strings.Repeat("remove ", len(profiles)), "")
			for _, profile := range profiles {
				requireServers(t, h.list(t, profile), false)
			}
		})
	})
}

// mcpStep runs agnthub and requires the listed actions (and mechanism, if given).
func (s *suite) mcpStep(t *testing.T, args []string, actions, mechanism string) {
	t.Helper()
	exit, stdout, stderr := s.agnthub(t, nil, args)
	if exit != 0 {
		t.Fatalf("agnthub %v: exit %d\n%s%s", args, exit, stdout, stderr)
	}
	var res mcpResult
	if err := sonic.Unmarshal(stdout, &res); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	var got []string
	for _, row := range res.Results {
		got = append(got, row.Action)
		if mechanism != "" && row.Mechanism != mechanism {
			t.Errorf(
				"%s %s: mechanism %q, want %q",
				row.Harness,
				row.Profile,
				row.Mechanism,
				mechanism,
			)
		}
	}
	if strings.Join(got, " ") != strings.TrimSpace(actions) {
		t.Fatalf("agnthub %v: actions %v, want %s\n%s", args, got, actions, stdout)
	}
	t.Logf("agnthub %s: %s", strings.Join(args, " "), strings.Join(got, " "))
}

// requireServers requires the neighbor server, and agnthub at the hosted URL
// when present (absent otherwise).
func requireServers(t *testing.T, servers mcpServers, present bool) {
	t.Helper()
	if _, ok := servers["other"]; !ok {
		t.Errorf("the neighbor server is gone: %v", servers)
	}
	url, ok := servers["agnthub"]
	switch {
	case present && !ok:
		t.Errorf("the Harness does not list agnthub: %v", servers)
	case present && url != "" && url != mcpURL:
		t.Errorf("the Harness lists agnthub at %s", url)
	case !present && ok:
		t.Errorf("the Harness still lists agnthub: %v", servers)
	default:
		t.Logf("the Harness lists %v", servers)
	}
}

func mcpOutput(t *testing.T, extraEnv []string, name string, args ...string) string {
	t.Helper()
	return string(harnessOutput(t, nativeContext{dir: elsewhereDir}, extraEnv, name, args...))
}

var claudeServer = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+): (\S+) \(HTTP\)`)

// listClaude: `claude mcp list` lists every server (its health check fails
// offline, after the listing).
func listClaude(t *testing.T, _ string) mcpServers {
	out := mcpOutput(t, nil, "claude", "mcp", "list")
	servers := mcpServers{}
	for _, m := range claudeServer.FindAllStringSubmatch(out, -1) {
		servers[m[1]] = m[2]
	}
	if len(servers) == 0 {
		t.Logf("claude mcp list:\n%s", out)
	}
	return servers
}

// listCodex: `codex mcp list --json`.
func listCodex(t *testing.T, _ string) mcpServers {
	out := mcpOutput(t, nil, "codex", "mcp", "list", "--json")
	var listed []struct {
		Name      string `json:"name"`
		Transport struct {
			URL         string            `json:"url"`
			HTTPHeaders map[string]string `json:"http_headers"`
		} `json:"transport"`
	}
	if !jsonArray([]byte(out), &listed) {
		t.Fatalf("codex mcp list --json:\n%s", out)
	}
	servers := mcpServers{}
	for _, s := range listed {
		servers[s.Name] = s.Transport.URL
	}
	return servers
}

var copilotServer = regexp.MustCompile(`(?m)^\s+([A-Za-z0-9_.-]+) \((\w+)\)`)

// listCopilot: `copilot mcp list` names the servers, `copilot mcp get` shows
// agnthub's URL.
func listCopilot(t *testing.T, _ string) mcpServers {
	out := mcpOutput(t, nil, "copilot", "mcp", "list")
	servers := mcpServers{}
	for _, m := range copilotServer.FindAllStringSubmatch(out, -1) {
		servers[m[1]] = ""
	}
	if _, ok := servers["agnthub"]; ok {
		get := mcpOutput(t, nil, "copilot", "mcp", "get", "agnthub")
		if m := regexp.MustCompile(`URL: (\S+)`).FindStringSubmatch(get); m != nil {
			servers["agnthub"] = m[1]
		}
	}
	return servers
}

var cursorServer = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+): `)

// listCursor: `cursor-agent mcp list` names every configured server (each
// fails to connect offline).
func listCursor(t *testing.T, _ string) mcpServers {
	out := mcpOutput(t, nil, "cursor-agent", "mcp", "list")
	servers := mcpServers{}
	for _, m := range cursorServer.FindAllStringSubmatch(out, -1) {
		servers[m[1]] = ""
	}
	return servers
}

var geminiServer = regexp.MustCompile(`(?m)^\S+ ([A-Za-z0-9_.-]+): (\S+) \((\w+)\)`)

// listGemini: `gemini mcp list` lists the configured servers (disabled outside
// a trusted folder, which does not matter here).
func listGemini(t *testing.T, _ string) mcpServers {
	out := mcpOutput(t, nil, "gemini", "mcp", "list")
	servers := mcpServers{}
	for _, m := range geminiServer.FindAllStringSubmatch(out, -1) {
		servers[m[1]] = m[2]
	}
	return servers
}

var opencodeServer = regexp.MustCompile(`(?m)^\S+ ([A-Za-z0-9_.-]+)\s`)

// listOpenCode: `opencode mcp list` asks OpenCode's background service, which
// loads its servers lazily: the listing is retried until the neighbor shows, and
// the service is stopped so the next listing reads the configuration again.
func listOpenCode(t *testing.T, _ string) mcpServers {
	defer mcpOutput(t, nil, "opencode", "service", "stop")
	var out string
	for range 30 {
		out = mcpOutput(t, nil, "opencode", "mcp", "list")
		servers := mcpServers{}
		for _, m := range opencodeServer.FindAllStringSubmatch(out, -1) {
			servers[m[1]] = ""
		}
		if _, ok := servers["other"]; ok {
			opencodeURLs(t, servers)
			return servers
		}
		<-time.After(time.Second)
	}
	t.Fatalf("opencode mcp list never listed the neighbor server:\n%s", out)
	return nil
}

// opencodeURLs adds each listed server's URL from the configuration OpenCode
// resolved (`opencode debug config`).
func opencodeURLs(t *testing.T, servers mcpServers) {
	t.Helper()
	out := mcpOutput(t, nil, "opencode", "debug", "config")
	var sources []struct {
		Info struct {
			MCP struct {
				Servers map[string]struct {
					URL string `json:"url"`
				} `json:"servers"`
			} `json:"mcp"`
		} `json:"info"`
	}
	if !jsonArray([]byte(out), &sources) {
		t.Fatalf("opencode debug config:\n%s", out)
	}
	for _, source := range sources {
		for name, server := range source.Info.MCP.Servers {
			if _, ok := servers[name]; ok {
				servers[name] = server.URL
			}
		}
	}
}

// ompMCPLoader runs omp's own MCP configuration loader (loadAllMCPConfigs, what
// omp connects to at startup) in the profile; omp has no listing command.
const ompMCPLoader = `
const { loadAllMCPConfigs } = await import(process.env.OMP_PACKAGE + "/src/mcp/config.ts");
const result = await loadAllMCPConfigs(process.cwd());
console.log(JSON.stringify(Object.fromEntries(
  Object.entries(result.configs).map(([name, config]) => [name, config.url ?? config.command ?? ""]))));
`

// listOMP: the servers omp loads in profile ("" for the default profile).
func listOMP(t *testing.T, profile string) mcpServers {
	pkg := ompPackage(t)
	script := filepath.Join(t.TempDir(), "load.ts")
	if err := os.WriteFile(script, []byte(ompMCPLoader), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"OMP_PACKAGE=" + pkg}
	if profile != "" {
		env = append(env, "OMP_PROFILE="+profile)
	}
	out := mcpOutput(t, env, "bun", script)
	servers := mcpServers{}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err := sonic.UnmarshalString(lines[len(lines)-1], &servers); err != nil {
		t.Fatalf("omp's MCP loader: %v\n%s", err, out)
	}
	return servers
}

// ompPackage is the installed omp package directory.
func ompPackage(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("omp")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	for dir := filepath.Dir(resolved); dir != "/"; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "src", "mcp", "config.ts")); err == nil {
			return dir
		}
	}
	t.Fatalf("no omp package above %s", resolved)
	return ""
}
