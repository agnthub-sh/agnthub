package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/bytedance/sonic"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/agnthub-sh/agnthub/adoption"
	"github.com/agnthub-sh/agnthub/internal/mcpserver"
	"github.com/agnthub-sh/agnthub/skills"
)

func (a *App) mcpCommand(ctx context.Context) *cobra.Command {
	var apiURL string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve agnthub to AI agents over MCP (stdio)",
		Long: `Run agnthub's Model Context Protocol server on stdin/stdout, for MCP clients
such as Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Copilot and omp.

Read-only tools search the agnthub registry (search_agents, get_agent,
get_repository) and build install commands (install_command) through the public
API. install_agents installs agents locally with the same plan, conflict and
ownership protections as agnthub add: it only previews unless called with
confirm, and honors DO_NOT_TRACK / DISABLE_TELEMETRY.`,
		Example: `  claude mcp add agnthub -- npx -y agnthub mcp
  agnthub mcp --api-url http://localhost:8080`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runMCP(ctx, apiURL)
		},
	}
	cmd.Flags().StringVar(&apiURL, "api-url", "",
		"registry API origin (default: $AGNTHUB_API_URL, the configuration's apiURL, "+
			"or "+adoption.DefaultAPIURL+")")
	return cmd
}

func (a *App) runMCP(ctx context.Context, apiURL string) error {
	reporter := a.Adoption
	if apiURL == "" {
		configured, err := skills.LoadAPIURL(skills.ConfigPath(a.Env))
		if err != nil {
			return err
		}
		apiURL = adoption.APIURL(a.Env, configured)
	} else {
		// Installs made through MCP report to the registry the tools read.
		withURL := *a.Adoption
		withURL.APIURL = apiURL
		reporter = &withURL
	}
	if u, err := url.Parse(apiURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Host == "" {
		return fmt.Errorf("invalid API URL %q: want an http(s) origin", apiURL)
	}
	server := mcpserver.New(&mcpserver.Client{
		BaseURL:   apiURL,
		HTTP:      adoption.HTTPClient(a.Env),
		UserAgent: "agnthub/" + Version,
	}, mcpserver.Options{Version: Version, Instructions: installInstructions})
	local := *a
	local.Adoption = reporter
	local.addInstallTool(server)
	return server.Run(ctx, &mcp.IOTransport{
		Reader: io.NopCloser(a.Stdin),
		Writer: nopWriteCloser{a.Stdout},
	})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

const installInstructions = `install_agents installs agents on this machine with the agnthub ` +
	`CLI. Always call it first without confirm to preview the plan, show the user what would be ` +
	`written where, and call it again with confirm: true only after the user explicitly approves.`

type installInput struct {
	Source     string   `json:"source"                jsonschema:"GitHub owner/repo[#ref], a Git URL or an absolute local directory"`
	Agents     []string `json:"agents"                jsonschema:"agent ids to install, or [\"*\"] for every agent"`
	Harnesses  []string `json:"harnesses"             jsonschema:"harnesses to install into, or [\"*\"] for every supported harness"`
	Scope      string   `json:"scope,omitempty"       jsonschema:"project (default: files go under project_dir) or global (the user's harness configuration)"`
	Extra      []string `json:"extra,omitempty"       jsonschema:"harness settings <harness>.<key>=<value>, e.g. omp.profile=work"`
	ProjectDir string   `json:"project_dir,omitempty" jsonschema:"absolute project directory; default: the directory this server runs in"`
	Confirm    bool     `json:"confirm,omitempty"     jsonschema:"apply the plan; only after the user approved the preview. Without it nothing is written"`
}

// installOutput is install_agents' result.
type installOutput struct {
	// Status is preview, applied, refused or failed.
	Status     string `json:"status"`
	ProjectDir string `json:"project_dir"`
	Message    string `json:"message"`
	// Plan is the plan agnthub add --json reports.
	Plan any `json:"plan,omitempty"`
}

func (a *App) addInstallTool(server *mcp.Server) {
	open := true
	mcp.AddTool(server, &mcp.Tool{
		Name:  "install_agents",
		Title: "Install agents (agnthub add)",
		Description: "Plans installing agents into coding harnesses on this machine exactly like " +
			"`agnthub add --json`: dependencies, required skills, harness settings, the lock file " +
			"and ownership records. Without confirm it only previews and writes nothing. With " +
			"confirm it applies the plan only if every item is valid: it never overwrites files " +
			"agnthub does not own or that were modified locally. Ask the user before confirming.",
		InputSchema: mcpserver.Schema[installInput](func(p map[string]*jsonschema.Schema) {
			p["source"].MinLength = jsonschema.Ptr(1)
			p["source"].MaxLength = jsonschema.Ptr(512)
			p["agents"].MinItems = jsonschema.Ptr(1)
			p["agents"].MaxItems = jsonschema.Ptr(64)
			p["agents"].Items.Pattern = `^(\*|[a-z0-9][a-z0-9_-]{0,63})$`
			p["harnesses"].MinItems = jsonschema.Ptr(1)
			p["harnesses"].UniqueItems = true
			p["harnesses"].Items.Enum = append(mcpserver.HarnessEnum(), allHarnesses, allSelector)
			p["scope"].Enum = []any{"project", "global"}
			p["scope"].Default = []byte(`"project"`)
			p["extra"].MaxItems = jsonschema.Ptr(16)
			p["project_dir"].MaxLength = jsonschema.Ptr(4096)
		}),
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &open},
	}, a.installAgents)
}

// installAgents runs agnthub add non-interactively with JSON output into a buffer: a
// preview without confirm, --yes with it. Nothing reaches the MCP stream but the result.
func (a *App) installAgents(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in installInput,
) (*mcp.CallToolResult, *installOutput, error) {
	workDir := a.WorkDir
	if in.ProjectDir != "" {
		if !filepath.IsAbs(in.ProjectDir) {
			return nil, nil, fmt.Errorf("project_dir %q must be an absolute path", in.ProjectDir)
		}
		workDir = filepath.Clean(in.ProjectDir)
		if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
			return nil, nil, fmt.Errorf("project_dir %q is not a directory", in.ProjectDir)
		}
	}
	var stdout, stderr bytes.Buffer
	run := *a
	run.WorkDir, run.Stdout, run.Stderr = workDir, &stdout, &stderr
	run.Interactive, run.styledStderr = false, false
	err := run.runAdd(ctx, in.Source, &addOptions{
		agents:    in.Agents,
		harnesses: in.Harnesses,
		extras:    in.Extra,
		global:    in.Scope == "global",
		json:      true,
		yes:       in.Confirm,
	})
	var plan struct {
		Status string `json:"status"`
	}
	var payload any
	if stdout.Len() > 0 {
		if decodeErr := sonic.Unmarshal(stdout.Bytes(), &payload); decodeErr != nil {
			return nil, nil, fmt.Errorf("reading the plan: %w", decodeErr)
		}
		_ = sonic.Unmarshal(stdout.Bytes(), &plan)
	}
	out := &installOutput{ProjectDir: workDir, Plan: payload, Status: plan.Status}
	switch {
	case plan.Status == statusPlanned && !in.Confirm:
		out.Status = "preview"
		out.Message = "Preview only; nothing was written. Show this plan to the user and call " +
			"install_agents again with confirm: true only after they approve."
		return nil, out, nil
	case plan.Status == statusApplied && err == nil:
		out.Message = "Applied: the plan's items were installed and recorded in the lock."
		return nil, out, nil
	case plan.Status == statusRefused:
		out.Message = "Refused: some items are blocked (see each item's reason, e.g. a file " +
			"agnthub does not own, a local modification or an incompatible harness). Nothing " +
			"was written. Change the selection or resolve the conflicts first."
	case payload == nil:
		if err == nil {
			err = errors.New("agnthub add produced no plan")
		}
		return nil, nil, err
	default:
		out.Message = "Failed"
		if err != nil {
			var exit *ExitError
			if !errors.As(err, &exit) {
				out.Message += ": " + err.Error()
			}
		}
		if detail := bytes.TrimSpace(stderr.Bytes()); len(detail) > 0 {
			out.Message += ": " + string(detail)
		}
	}
	return &mcp.CallToolResult{IsError: true}, out, nil
}
