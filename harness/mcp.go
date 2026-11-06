package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
)

// MCPUserConfigFiles returns the user-scope files a Harness reads MCP server
// definitions from, as the Harness resolves them (CLAUDE_CONFIG_DIR, CODEX_HOME,
// COPILOT_HOME, GEMINI_CLI_HOME, OPENCODE_CONFIG_DIR / XDG_CONFIG_HOME, omp's
// PI_CONFIG_DIR and PI_CODING_AGENT_DIR), the preferred file to write first.
// Only OpenCode has several: it merges opencode.json, opencode.jsonc and the older
// config.json from its configuration directory. profile selects an omp profile
// ("" or "default" for the default profile; a named profile must exist) and is
// ignored by every other Harness.
func MCPUserConfigFiles(ctx Context, id agent.HarnessID, profile string) ([]string, error) {
	home := ctx.Env.Home()
	switch id {
	case agent.Claude:
		if err := requireAbsoluteDir(ctx, "CLAUDE_CONFIG_DIR", "Claude Code"); err != nil {
			return nil, err
		}
		// Claude Code keeps its user configuration, MCP servers included, in
		// .claude.json beside ~/.claude, or inside CLAUDE_CONFIG_DIR when set.
		if dir := ctx.Env.Get("CLAUDE_CONFIG_DIR"); dir != "" {
			return []string{filepath.Join(dir, ".claude.json")}, nil
		}
		return requireHome(home, filepath.Join(home, ".claude.json"))
	case agent.OpenCode:
		dir := opencodeConfigDir(ctx)
		return requireHome(home,
			filepath.Join(dir, "opencode.json"),
			filepath.Join(dir, "opencode.jsonc"),
			filepath.Join(dir, "config.json"),
		)
	case agent.Codex:
		if err := requireAbsoluteDir(ctx, "CODEX_HOME", "Codex"); err != nil {
			return nil, err
		}
		return requireHome(home, filepath.Join(codexHome(ctx), "config.toml"))
	case agent.Copilot:
		if err := requireAbsoluteDir(ctx, "COPILOT_HOME", "Copilot CLI"); err != nil {
			return nil, err
		}
		return requireHome(home, filepath.Join(copilotHome(ctx), "mcp-config.json"))
	case agent.Cursor:
		return requireHome(home, filepath.Join(home, ".cursor", "mcp.json"))
	case agent.Gemini:
		if err := requireAbsoluteDir(ctx, "GEMINI_CLI_HOME", "Gemini CLI"); err != nil {
			return nil, err
		}
		return requireHome(home, filepath.Join(geminiConfigDir(ctx), "settings.json"))
	case agent.OMP:
		dir, err := ompMCPDir(ctx, profile)
		if err != nil {
			return nil, err
		}
		return []string{filepath.Join(dir, "mcp.json")}, nil
	default:
		return nil, fmt.Errorf("unknown harness %q", id)
	}
}

// requireHome returns paths when the home directory they derive from is known.
func requireHome(home string, paths ...string) ([]string, error) {
	if home == "" {
		return nil, errors.New("HOME is not set")
	}
	return paths, nil
}

// ompMCPDir is the agent directory of one omp profile, where omp reads the
// profile's user mcp.json. agnthub never initializes a named profile. For the
// default profile, PI_CODING_AGENT_DIR relocates the directory unless it is a
// named profile's: omp propagates that value to the processes a profile's
// session starts, and the default profile does not read it.
func ompMCPDir(ctx Context, profile string) (string, error) {
	name, err := normalizeOMPProfile(profile)
	if err != nil {
		return "", err
	}
	root, err := ompConfigRoot(ctx)
	if err != nil {
		return "", err
	}
	if name == "" {
		override := ctx.Env.Get("PI_CODING_AGENT_DIR")
		if !filepath.IsAbs(override) {
			return filepath.Join(root, "agent"), nil
		}
		override = filepath.Clean(override)
		if rel, relErr := filepath.Rel(filepath.Join(root, "profiles"), override); relErr == nil {
			if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) == 2 &&
				parts[0] != ".." && parts[1] == "agent" {
				return filepath.Join(root, "agent"), nil
			}
		}
		return override, nil
	}
	profileRoot := ompProfileRoot(root, name)
	if info, statErr := os.Stat(profileRoot); statErr != nil || !info.IsDir() {
		return "", fmt.Errorf("omp profile %q does not exist at %s; agnthub does not "+
			"initialize omp profiles (run `omp --profile %s` once)", name, profileRoot, name)
	}
	return filepath.Join(profileRoot, "agent"), nil
}

// OMPProfiles lists the named omp profiles that exist, sorted; the default
// profile always exists and is not listed.
func OMPProfiles(ctx Context) ([]string, error) {
	root, err := ompConfigRoot(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "profiles"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ompDefaultProfile ||
			validateOMPProfile(entry.Name()) != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names, nil
}
