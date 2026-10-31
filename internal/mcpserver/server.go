// Package mcpserver is agnthub's Model Context Protocol server: read-only tools over the
// public registry API (search, agent and repository details, install commands). The CLI adds
// its local install tool on top and serves it over stdio (`agnthub mcp`).
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/agnthub-sh/agnthub/agent"
)

// Output bounds.
const (
	// MaxPerPage caps search results per call.
	MaxPerPage     = 20
	defaultPerPage = 10
	// MaxBody caps each instructions body get_agent returns.
	MaxBody = 32 << 10
	// MaxRepositoryAgents caps the Agents get_repository lists.
	MaxRepositoryAgents = 50
	maxAgents           = 50
)

// Patterns the registry API enforces on its path parameters.
const (
	RepositoryPattern = `^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`
	AgentPattern      = `^[a-z0-9][a-z0-9_-]{0,63}$`
)

// sorts are the registry's search orders.
var sorts = []any{
	"relevance", "recent", "installs", "installs_30d", "installs_7d", "harness_installs", "stars",
}

// HarnessEnum lists the Harness ids for input schemas.
func HarnessEnum() []any {
	out := make([]any, len(agent.AllHarnesses))
	for i, h := range agent.AllHarnesses {
		out[i] = string(h)
	}
	return out
}

const instructions = `agnthub (https://agnthub.sh) is a registry of AI coding agents (subagent ` +
	`definitions) published in public GitHub repositories, installable into Claude Code (claude), ` +
	`OpenCode (opencode), Codex (codex), GitHub Copilot CLI (copilot), Cursor (cursor), Gemini CLI ` +
	`(gemini) and omp (omp). Use search_agents to find agents, get_agent or get_repository to ` +
	`inspect them (installable harnesses with reasons, dependencies, reported installs, stars) and ` +
	`install_command for the exact command a user can run. Installs are counts of deduplicated ` +
	`CLI identities, not people, quality or safety.`

// Options configure the server.
type Options struct {
	Version string
	// Instructions are appended to the server instructions.
	Instructions string
}

// New returns a server with the read-only registry tools backed by client.
func New(client *Client, opts Options) *mcp.Server {
	text := instructions
	if opts.Instructions != "" {
		text += "\n\n" + opts.Instructions
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "agnthub", Title: "agnthub", Version: opts.Version},
		&mcp.ServerOptions{Instructions: text},
	)
	t := &tools{client: client}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_agents",
		Title:       "Search agnthub agents",
		Description: "Full-text search over agents currently listed on agnthub. Every result is an installable agent of a public GitHub repository, with the harnesses it installs into, its license policy, reported installs and agnthub stars.",
		InputSchema: Schema[searchInput](func(p map[string]*jsonschema.Schema) {
			p["query"].MaxLength = jsonschema.Ptr(200)
			harnessList(p["harnesses"])
			p["sort"].Enum = sorts
			p["page"].Minimum, p["page"].Maximum = jsonschema.Ptr(1.0), jsonschema.Ptr(50.0)
			p["per_page"].Minimum = jsonschema.Ptr(1.0)
			p["per_page"].Maximum = jsonschema.Ptr(float64(MaxPerPage))
			p["per_page"].Default = []byte("10")
		}),
		Annotations: readOnly,
	}, t.search)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_agent",
		Title:       "Get an agnthub agent",
		Description: "Details of one listed agent: description, the harnesses it installs into (or why it cannot), its agent and skill dependencies, reported installs and agnthub stars, plus its repository's license and indexed commit. Instructions are included only on request and only when the license allows publishing them.",
		InputSchema: Schema[agentInput](func(p map[string]*jsonschema.Schema) {
			p["repository"].Pattern = RepositoryPattern
			p["agent"].Pattern = AgentPattern
		}),
		Annotations: readOnly,
	}, t.agent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_repository",
		Title:       "Get an agnthub repository",
		Description: "A listed GitHub repository with its agents (installable harnesses, dependencies, installs), license, GitHub stats and scan problems. Instructions are never included; use get_agent for them.",
		InputSchema: Schema[repositoryInput](func(p map[string]*jsonschema.Schema) {
			p["repository"].Pattern = RepositoryPattern
		}),
		Annotations: readOnly,
	}, t.repository)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "install_command",
		Title:       "Build an agnthub install command",
		Description: "Builds the `npx agnthub add …` command that installs agents of a listed repository, exactly as agnthub.sh's install builder does (every supported harness selected becomes `--harness all`). Checks that the repository is listed and the agents exist, and warns about harnesses an agent cannot be installed into. Empty agents or harnesses let the CLI ask interactively.",
		InputSchema: Schema[commandInput](func(p map[string]*jsonschema.Schema) {
			p["repository"].Pattern = RepositoryPattern
			p["agents"].MaxItems = jsonschema.Ptr(maxAgents)
			p["agents"].UniqueItems = true
			p["agents"].Items.Pattern = AgentPattern
			harnessList(p["harnesses"])
			p["scope"].Enum = []any{"project", "global"}
			p["scope"].Default = []byte(`"project"`)
			p["omp_profile"].MaxLength = jsonschema.Ptr(64)
		}),
		Annotations: readOnly,
	}, t.command)
	return server
}

// Schema infers T's input schema and lets adjust add constraints to its properties.
func Schema[T any](adjust func(props map[string]*jsonschema.Schema)) *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err) // a programming error in a static input type
	}
	adjust(s.Properties)
	return s
}

func harnessList(s *jsonschema.Schema) {
	s.Items.Enum = HarnessEnum()
	s.MaxItems = jsonschema.Ptr(len(agent.AllHarnesses))
	s.UniqueItems = true
}

type tools struct {
	client *Client
}

type searchInput struct {
	Query     string   `json:"query,omitempty"     jsonschema:"words to match in agent names, descriptions, repositories and instructions; empty lists recent agents"`
	Harnesses []string `json:"harnesses,omitempty" jsonschema:"only agents installable into every listed harness"`
	Sort      string   `json:"sort,omitempty"      jsonschema:"relevance (default with a query), recent (default without), installs, installs_30d, installs_7d, harness_installs (needs exactly one harness) or stars (agnthub stars)"`
	Page      int      `json:"page,omitempty"`
	PerPage   int      `json:"per_page,omitempty"`
}

func (t *tools) search(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in searchInput,
) (*mcp.CallToolResult, *SearchPage, error) {
	if in.Sort == "harness_installs" && len(in.Harnesses) != 1 {
		return nil, nil, errors.New("sort harness_installs needs exactly one harness")
	}
	perPage := in.PerPage
	if perPage == 0 {
		perPage = defaultPerPage
	}
	page, err := t.client.Search(ctx, &SearchQuery{
		Text: in.Query, Harnesses: in.Harnesses, Sort: in.Sort, Page: in.Page, PerPage: perPage,
	})
	if err != nil {
		return nil, nil, err
	}
	return nil, page, nil
}

type agentInput struct {
	Repository          string `json:"repository"                     jsonschema:"GitHub owner/repo"`
	Agent               string `json:"agent"                          jsonschema:"agent id within the repository"`
	IncludeInstructions bool   `json:"include_instructions,omitempty" jsonschema:"include the instructions (bodies of the portable definition and native variants), each capped at 32 KiB; only licensed repositories publish them"`
}

func (t *tools) agent(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in agentInput,
) (*mcp.CallToolResult, *AgentDetail, error) {
	owner, repo := splitRepository(in.Repository)
	detail, err := t.client.Agent(ctx, owner, repo, in.Agent)
	if err != nil {
		return nil, nil, err
	}
	boundBodies(&detail.Agent, in.IncludeInstructions)
	return nil, detail, nil
}

type repositoryInput struct {
	Repository string `json:"repository" jsonschema:"GitHub owner/repo"`
}

func (t *tools) repository(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in repositoryInput,
) (*mcp.CallToolResult, *Repository, error) {
	owner, repo := splitRepository(in.Repository)
	out, err := t.client.Repository(ctx, owner, repo)
	if err != nil {
		return nil, nil, err
	}
	if len(out.Agents) > MaxRepositoryAgents {
		out.AgentsOmitted = len(out.Agents) - MaxRepositoryAgents
		out.Agents = out.Agents[:MaxRepositoryAgents]
	}
	for i := range out.Agents {
		boundBodies(&out.Agents[i], false)
	}
	return nil, out, nil
}

type commandInput struct {
	Repository string   `json:"repository"            jsonschema:"GitHub owner/repo"`
	Agents     []string `json:"agents,omitempty"      jsonschema:"agent ids to install; empty lets the CLI ask"`
	Harnesses  []string `json:"harnesses,omitempty"   jsonschema:"harnesses to install into; empty lets the CLI ask"`
	All        bool     `json:"all,omitempty"         jsonschema:"every agent into every supported harness without prompting (--all); excludes agents and harnesses"`
	Scope      string   `json:"scope,omitempty"       jsonschema:"project (default: the current project) or global (the user's harness configuration)"`
	OMPProfile string   `json:"omp_profile,omitempty" jsonschema:"omp profile to install into (applies when omp is selected); default uses omp's current profile"`
}

// CommandOutput is install_command's result.
type CommandOutput struct {
	Command string `json:"command"`
	// Warnings name selected agent/harness pairs the CLI would refuse.
	Warnings []string `json:"warnings,omitempty"`
}

func (t *tools) command(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in commandInput,
) (*mcp.CallToolResult, *CommandOutput, error) {
	if in.All && (len(in.Agents) > 0 || len(in.Harnesses) > 0) {
		return nil, nil, errors.New(
			"all selects every agent and every supported harness; drop agents and harnesses",
		)
	}
	owner, repo := splitRepository(in.Repository)
	listed, err := t.client.Repository(ctx, owner, repo)
	if err != nil {
		return nil, nil, err
	}
	harnesses := make([]agent.HarnessID, len(in.Harnesses))
	for i, h := range in.Harnesses {
		harnesses[i] = agent.HarnessID(h)
	}
	command, err := BuildCommand(&CommandInput{
		Source:     listed.FullName,
		Agents:     in.Agents,
		Harnesses:  harnesses,
		All:        in.All,
		Global:     in.Scope == "global",
		OMPProfile: in.OMPProfile,
	})
	if err != nil {
		return nil, nil, err
	}
	warnings, err := commandWarnings(listed, in.Agents, in.Harnesses)
	if err != nil {
		return nil, nil, err
	}
	return nil, &CommandOutput{Command: command, Warnings: warnings}, nil
}

// commandWarnings rejects agents the repository does not list and warns about selected
// harnesses an agent cannot be installed into.
func commandWarnings(listed *Repository, ids, harnesses []string) ([]string, error) {
	var warnings []string
	for _, id := range ids {
		i := slices.IndexFunc(listed.Agents, func(a Agent) bool { return a.ID == id })
		if i < 0 {
			available := make([]string, len(listed.Agents))
			for j := range listed.Agents {
				available[j] = listed.Agents[j].ID
			}
			return nil, fmt.Errorf("agent %q is not listed in %s (available: %s)",
				id, listed.FullName, strings.Join(available, ", "))
		}
		for _, target := range listed.Agents[i].Targets {
			if !slices.Contains(harnesses, target.Harness) ||
				target.Status == "native" || target.Status == "generated" {
				continue
			}
			reason := target.Status
			if target.Reason != nil {
				reason += ": " + *target.Reason
			}
			warnings = append(warnings, fmt.Sprintf(
				"%s cannot be installed for %s (%s); the CLI will refuse that pair",
				id, target.Harness, reason))
		}
	}
	return warnings, nil
}

// splitRepository splits an owner/repo the input schema already validated.
func splitRepository(full string) (owner, repo string) {
	owner, repo, _ = strings.Cut(full, "/")
	return owner, repo
}

// boundBodies drops instructions unless requested and caps each kept body at MaxBody.
func boundBodies(a *Agent, keep bool) {
	bound := func(f *SourceFile) {
		if f.Body == nil {
			return
		}
		if !keep {
			f.Body = nil
			return
		}
		if body := *f.Body; len(body) > MaxBody {
			cut := MaxBody
			for cut > 0 && !utf8.RuneStart(body[cut]) {
				cut--
			}
			body = body[:cut]
			f.Body, f.BodyTruncated = &body, true
		}
	}
	if a.Portable != nil {
		bound(a.Portable)
	}
	for i := range a.Variants {
		bound(&a.Variants[i].SourceFile)
	}
}
