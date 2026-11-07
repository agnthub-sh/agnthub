package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/internal/apiclient"
	"github.com/agnthub-sh/agnthub/internal/mcpsetup"
	"github.com/agnthub-sh/agnthub/skills"
)

// mcpRemoved tells MCP client configurations that still start `agnthub mcp` where the server
// went: the local MCP server no longer exists.
const mcpRemoved = "The local MCP server was removed in v0.5.0. Use the hosted server instead: " +
	"https://mcp.agnthub.sh (setup: https://app.agnthub.sh/mcp)."

const mcpInstallHint = "Run `npx agnthub-cli mcp install` to configure the hosted server."

// mcpCommand groups install and uninstall. Run bare, it still answers the MCP client
// configurations that start the removed local server (`npx agnthub-cli mcp`, whatever
// flags they pass): with no terminal on stdin it explains the removal and exits non-zero,
// so the client reports the server as failed; in a terminal it shows its help.
func (a *App) mcpCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Set up agnthub's hosted MCP server in your harnesses",
		Long: `Set up agnthub's hosted MCP server (` + mcpsetup.URL + `) in your harnesses, so
AI agents can search the registry and install agents for you.

install adds the server to every harness you choose (and every omp profile), using
the harness's own mcp command where it has a reliable one and otherwise editing its
configuration file: only the agnthub entry changes, the previous file is backed up
and the new one is written atomically. uninstall removes it again. Sign-in is with
GitHub in each harness (OAuth); no secret is written unless you pass --api-key.

The local MCP server (` + "`agnthub mcp`" + ` as a stdio server) was removed in v0.5.0.`,
		Example: `  agnthub mcp install
  agnthub mcp install --all --yes
  agnthub mcp install --harness omp --profile work --yes
  agnthub mcp uninstall`,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if slices.ContainsFunc(
				args,
				func(arg string) bool { return arg == "-h" || arg == "--help" },
			) ||
				readerIsTerminal(a.Stdin) {
				return cmd.Help()
			}
			fmt.Fprintln(a.Stderr, mcpRemoved)
			fmt.Fprintln(a.Stderr, mcpInstallHint)
			return &ExitError{Code: 1}
		},
	}
	cmd.AddCommand(a.mcpInstallCommand(ctx), a.mcpUninstallCommand(ctx))
	return cmd
}

func readerIsTerminal(r io.Reader) bool {
	f, ok := r.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(f.Fd())
}

type mcpOptions struct {
	harnesses []string
	profiles  []string
	scope     string
	apiKey    bool
	force     bool
	all       bool
	yes       bool
	json      bool
}

// register declares the flags install and uninstall share; all describes --all.
func (o *mcpOptions) register(cmd *cobra.Command, all string) {
	flags := cmd.Flags()
	flags.StringSliceVarP(&o.harnesses, "harness", "H", nil,
		"harnesses, comma-separated or repeated: "+harnessList())
	flags.StringArrayVar(&o.profiles, "profile", nil,
		"omp profile (repeatable; \"default\" for the default profile); implies --harness omp")
	flags.StringVar(
		&o.scope,
		"scope",
		"user",
		"user, or project for the current project's configuration",
	)
	flags.BoolVar(&o.all, "all", false, all)
	flags.BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation")
	flags.BoolVar(&o.json, "json", false,
		"print the result as JSON (never prompts; without --harness it implies --all --yes)")
}

func harnessList() string {
	names := make([]string, 0, len(agent.AllHarnesses))
	for _, id := range agent.AllHarnesses {
		names = append(names, string(id))
	}
	return strings.Join(names, ", ")
}

const mcpChoosing = `

In a terminal, one harness is confirmed with a yes/no question; several are
offered in a toggle list, all preselected (space toggles, ctrl+a toggles all, enter
confirms), and the chosen set is confirmed. --all takes every one without the
list, --yes skips the confirmation, and --all --yes asks nothing. --harness and
--profile pick harnesses and omp profiles explicitly: no list, but still a
confirmation unless --yes. Without a terminal, pass --all or --harness: the run
then asks nothing. --json never prompts and, without --harness, behaves like
--all --yes.`

const mcpExitCodes = `

Exit status: 0 when every chosen harness is set up (or all already were); 1 on an
error (invalid flags, a declined or cancelled prompt, no --all or --harness without
a terminal, or a harness whose configuration could not be changed; the others are
still done); 2 when a harness was skipped because its agnthub entry was not set up
by agnthub (rerun with --force to replace it).`

func (a *App) mcpInstallCommand(ctx context.Context) *cobra.Command {
	opts := &mcpOptions{}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Add agnthub's hosted MCP server to your harnesses",
		Long: `Add agnthub's hosted MCP server (` + mcpsetup.URL + `) to your harnesses.

install looks at the harnesses detected on this machine (and every omp profile) and
keeps the ones without agnthub's MCP server at ` + mcpsetup.URL + `; an older agnthub
entry (the former /mcp URLs, or the local server ` + "`npx agnthub-cli mcp`" + `) counts as
missing and is updated. The harnesses that already have it are never offered, only
named in one line; when every one has it, install says so and exits 0.` + mcpChoosing + `

Each harness gets an "agnthub" server (Streamable HTTP, ` + mcpsetup.URL + `) in its
user configuration, or with --scope project in the current project's. An agnthub
entry agnthub did not write is replaced only once you confirm it, or with --force.
Claude Code, Copilot CLI and Gemini CLI are configured with their own mcp command
when it is on PATH; the others (and every --api-key or project-scope setup those
commands cannot do) by editing the file: only the agnthub entry changes, the
previous file is copied to ~/.local/state/agnthub/mcp-backups, and the new one is
written atomically with the old file mode (0600 for a new file). A configuration
that does not parse is never changed. Naming an undetected harness with --harness
sets it up anyway.

You then sign in with GitHub in each harness (install prints how). --api-key writes
your agnthub API key (` + apiclient.KeyVariable + `, or the one ` + "`agnthub auth login`" + ` saved) as an
Authorization header instead: the key is then stored in plain text in each
harness's configuration file.` + mcpExitCodes,
		Example: `  agnthub mcp install
  agnthub mcp install --all --yes
  agnthub mcp install --harness claude,codex --yes
  agnthub mcp install --json
  agnthub mcp install --harness omp --profile default --profile work --yes
  agnthub mcp install --harness cursor --scope project --yes
  agnthub mcp install --harness claude --api-key --yes`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runMCPInstall(ctx, opts)
		},
	}
	opts.register(cmd, "every detected harness (and omp profile) without agnthub's "+
		"MCP server, without the list")
	cmd.Flags().BoolVar(&opts.apiKey, "api-key", false,
		"write your agnthub API key as an Authorization header instead of signing in (stored in plain text)")
	cmd.Flags().
		BoolVar(&opts.force, "force", false, "replace an agnthub entry agnthub did not set up")
	return cmd
}

func (a *App) mcpUninstallCommand(ctx context.Context) *cobra.Command {
	opts := &mcpOptions{}
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove agnthub's MCP server from your harnesses",
		Long: `Remove agnthub's MCP server from your harnesses.

uninstall looks in every supported harness's user configuration (every omp profile
included) and, with --scope project, also in the current project's, for an agnthub
entry agnthub set up: the hosted server or an older form of it (the former /mcp URLs,
or the local server ` + "`npx agnthub-cli mcp`" + `). When there is none it says so and
exits 0.` + mcpChoosing + `

Removal uses the harness's own mcp command where install would, otherwise the same
careful file edit with a backup. Other servers, and an agnthub entry agnthub did
not write, are never removed.

Exit status: 0 when every chosen entry was removed, or none was found; 1 on an
error (invalid flags, a declined or cancelled prompt, no --all or --harness without
a terminal, or a configuration that could not be changed).`,
		Example: `  agnthub mcp uninstall
  agnthub mcp uninstall --all --yes
  agnthub mcp uninstall --harness omp --profile work --yes
  agnthub mcp uninstall --scope project --json`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runMCPUninstall(ctx, opts)
		},
	}
	opts.register(cmd, "every agnthub MCP install found, without the list")
	return cmd
}

// mcpSelection is the harnesses the flags name; nil when none is named.
type mcpSelection []agent.HarnessID

func (o *mcpOptions) selection() (mcpSelection, error) {
	var sel mcpSelection
	for _, name := range o.harnesses {
		name = strings.TrimSpace(name)
		if name == allSelector || name == allHarnesses {
			return nil, errors.New("--harness all was replaced by --all")
		}
		id, err := agent.ParseHarness(name)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(sel, id) {
			sel = append(sel, id)
		}
	}
	if o.all && len(sel) > 0 {
		return nil, errors.New("--all takes every harness; drop --harness or --all")
	}
	if len(o.profiles) > 0 {
		switch {
		case len(sel) == 0 && !o.all:
			sel = mcpSelection{agent.OMP}
		case len(sel) > 0 && !slices.Contains(sel, agent.OMP):
			return nil, errors.New("--profile selects omp profiles; add --harness omp")
		}
	}
	return sel, nil
}

// mcpFlow is how a run chooses among its candidates.
type mcpFlow struct {
	// list offers several candidates in a toggle list.
	list bool
	// confirm asks before changing anything.
	confirm bool
}

// mcpFlow decides the prompts: a toggle list without --all or a named harness,
// a confirmation without --yes, and neither without a terminal or with --json.
// Without a terminal, --all or --harness is required (--json implies --all).
func (a *App) mcpFlow(opts *mcpOptions, sel mcpSelection, noTerminal string) (mcpFlow, error) {
	named := len(sel) > 0
	all := opts.all || (opts.json && !named)
	if !a.Interactive && !all && !named {
		return mcpFlow{}, errors.New(noTerminal)
	}
	prompts := a.Interactive && !opts.json
	return mcpFlow{list: prompts && !all && !named, confirm: prompts && !opts.yes}, nil
}

// mcpQuestions words one command's prompts.
type mcpQuestions struct {
	// list titles the toggle list; one (with the target's label) and many the
	// confirmation.
	list, one, many string
	// note describes a target: its file and what changes.
	note func(*mcpsetup.Target) string
	// preselect reports a target selected in the list.
	preselect func(*mcpsetup.Target) bool
}

// chooseMCP returns the candidates the user chose: from a toggle list when the
// flow has one and there are several, then confirmed when the flow asks. A
// declined confirmation or an empty choice is errCancelled.
func (a *App) chooseMCP(
	ctx context.Context,
	out *printer,
	flow mcpFlow,
	candidates []*mcpsetup.Target,
	q mcpQuestions,
) ([]*mcpsetup.Target, error) {
	chosen := candidates
	listed := flow.list && len(candidates) > 1
	if listed {
		options := make([]Option, 0, len(candidates))
		labels := alignLabels(candidates, a.targetLabel)
		for i, t := range candidates {
			options = append(options, Option{
				Value:    strconv.Itoa(i),
				Label:    labels[i] + "  " + q.note(t),
				Selected: q.preselect(t),
			})
		}
		out.gapBeforePrompt()
		values, err := a.Prompter.MultiSelect(ctx, q.list,
			"Space toggles, ctrl+a toggles all, enter confirms.", options)
		if err != nil {
			return nil, err
		}
		chosen = make([]*mcpsetup.Target, 0, len(values))
		for _, value := range values {
			i, err := strconv.Atoi(value)
			if err != nil || i < 0 || i >= len(candidates) {
				return nil, fmt.Errorf("unexpected choice %q", value)
			}
			chosen = append(chosen, candidates[i])
		}
		if len(chosen) == 0 {
			return nil, errCancelled
		}
	}
	if !flow.confirm {
		return chosen, nil
	}
	title, description := q.many, ""
	if len(chosen) == 1 {
		title, description = fmt.Sprintf(q.one, shortLabel(chosen[0])), q.note(chosen[0])
	} else {
		lines := alignLabels(chosen, shortLabel)
		for i, t := range chosen {
			lines[i] += "  " + q.note(t)
		}
		description = strings.Join(lines, "\n")
	}
	if !listed {
		// A toggle list already ends with a blank line.
		out.gapBeforePrompt()
	}
	ok, err := a.Prompter.Confirm(ctx, title, description)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errCancelled
	}
	return chosen, nil
}

// alignLabels labels each target, padded to the widest so what follows lines up.
func alignLabels(targets []*mcpsetup.Target, label func(*mcpsetup.Target) string) []string {
	labels := make([]string, len(targets))
	width := 0
	for i, t := range targets {
		labels[i] = label(t)
		width = max(width, ansi.StringWidth(labels[i]))
	}
	for i := range labels {
		labels[i] += strings.Repeat(" ", width-ansi.StringWidth(labels[i]))
	}
	return labels
}

// mcpScope parses --scope; project scope is refused in the home directory, which
// is no project.
func (a *App) mcpScope(value string) (harness.Scope, error) {
	switch value {
	case "user", "global":
		return harness.ScopeGlobal, nil
	case "project":
		if a.inHome() {
			return "", errors.New("refusing to use project-scope MCP configuration in your home " +
				"directory, which is no project; run this in a project's directory, or use --scope user")
		}
		return harness.ScopeProject, nil
	default:
		return "", fmt.Errorf("--scope %q: use user or project", value)
	}
}

// mcpAPIKey is the key `agnthub auth status` reports: AGNTHUB_API_KEY, else the
// configuration's apiKey.
func (a *App) mcpAPIKey() (string, error) {
	configured, err := skills.LoadAPI(skills.ConfigPath(a.Env))
	if err != nil {
		return "", err
	}
	credential, err := apiclient.Resolve(a.Env, configured.Key)
	if err != nil {
		return "", err
	}
	if credential.Key == "" {
		return "", errors.New("--api-key: no agnthub API key; run `agnthub auth login` or set " +
			apiclient.KeyVariable + " (create one at " + apiclient.KeyPage + ")")
	}
	return credential.Key, nil
}

func (a *App) mcpSetup() *mcpsetup.Setup {
	return &mcpsetup.Setup{Ctx: a.harnessContext(), Run: a.RunHarness, Now: a.Now}
}

func (a *App) harnessName(id agent.HarnessID) string {
	if adapter, ok := a.Registry.Get(id); ok {
		return adapter.Name()
	}
	return string(id)
}

func (a *App) runMCPInstall(ctx context.Context, opts *mcpOptions) error {
	sel, err := opts.selection()
	if err != nil {
		return err
	}
	scope, err := a.mcpScope(opts.scope)
	if err != nil {
		return err
	}
	apiKey := ""
	if opts.apiKey {
		if scope == harness.ScopeProject {
			return errors.New("--api-key cannot be used with --scope project: the key would be " +
				"written into a project file that may be shared or committed")
		}
		if apiKey, err = a.mcpAPIKey(); err != nil {
			return err
		}
	}
	flow, err := a.mcpFlow(opts, sel, "no terminal to choose harnesses in: pass --all to set "+
		"up every detected harness without agnthub's MCP server, or choose with --harness "+
		"(e.g. --harness claude,codex)")
	if err != nil {
		return err
	}
	ids, undetected := []agent.HarnessID(sel), []agent.HarnessID(nil)
	if len(ids) == 0 {
		hctx := a.harnessContext()
		for _, installer := range a.Registry.Installers() {
			if installer.Detect(hctx) {
				ids = append(ids, installer.ID())
			} else {
				undetected = append(undetected, installer.ID())
			}
		}
	}
	setup := a.mcpSetup()
	targets, err := setup.Scan(ctx, ids, scope, opts.profiles, apiKey)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New(
			"no supported harness detected (" + harnessList() + "); install one, or " +
				"name it with --harness to set it up anyway",
		)
	}
	out := a.printer(opts.json)
	report := mcpReport{
		Command: "install",
		Scope:   scopeName(scope),
		URL:     mcpsetup.URL,
		apiKey:  apiKey != "",
		named:   len(sel) > 0,
	}
	for _, id := range undetected {
		report.NotDetected = append(report.NotDetected, string(id))
	}
	missing := make([]*mcpsetup.Target, 0, len(targets))
	for _, t := range targets {
		if t.Err == nil && t.State == mcpsetup.Installed {
			report.current = append(report.current, shortLabel(t))
		} else {
			missing = append(missing, t)
		}
	}
	var chosen []*mcpsetup.Target
	force := opts.force
	if len(missing) > 0 {
		if len(report.current) > 0 && (flow.confirm || flow.list && len(missing) > 1) {
			out.paragraph(0, out.styles.dim.Render(alreadySetUp(report.current)))
			report.currentShown = true
		}
		chosen, err = a.chooseMCP(ctx, out, flow, missing, mcpQuestions{
			list: "Harnesses to set up agnthub's MCP server in",
			one:  "Add agnthub's MCP server to %s?",
			many: "Add agnthub's MCP server to these harnesses?",
			note: func(t *mcpsetup.Target) string {
				path := out.path(t.Path)
				switch {
				case t.Err != nil:
					return path + " · cannot read its configuration"
				case t.State == mcpsetup.Foreign:
					return path + " · replace another agnthub server (" + t.Existing + ")"
				case t.State != mcpsetup.Absent:
					return path + " · update an older agnthub setup"
				}
				return path
			},
			preselect: func(t *mcpsetup.Target) bool { return t.Err == nil },
		})
		if err != nil {
			return err
		}
		// Confirming a foreign entry's replacement is the --force.
		force = force || flow.confirm && slices.ContainsFunc(chosen,
			func(t *mcpsetup.Target) bool { return t.State == mcpsetup.Foreign })
	}
	if apiKey != "" && len(chosen) > 0 {
		fmt.Fprintln(
			a.Stderr,
			"warning: --api-key stores your agnthub API key in plain text in each "+
				"selected harness's MCP configuration; anyone who can read those files can use it",
		)
	}
	// Rows follow the scan; already set-up ones are reported unchanged.
	for _, t := range targets {
		switch {
		case slices.Contains(chosen, t):
			report.add(t, setup.Install(ctx, t, force))
		case t.Err == nil && t.State == mcpsetup.Installed:
			report.add(t, mcpsetup.Result{Action: mcpsetup.ActionUnchanged})
		}
	}
	if apiKey != "" {
		a.warnReadable(report.Results)
	}
	return a.emitMCP(out, &report)
}

// targetLabel names a target in a toggle list: its harness ID and name, and
// profile.
func (a *App) targetLabel(t *mcpsetup.Target) string {
	label := fmt.Sprintf("%-9s %s", t.Harness, a.harnessName(t.Harness))
	if t.Profile != "" {
		label += " · profile " + t.Profile
	}
	if t.Scope == harness.ScopeProject {
		label += " (project)"
	}
	return label
}

// shortLabel names a target in a sentence: "claude", "omp · work".
func shortLabel(t *mcpsetup.Target) string {
	label := string(t.Harness)
	if t.Profile != "" {
		label += " · " + t.Profile
	}
	if t.Scope == harness.ScopeProject {
		label += " (project)"
	}
	return label
}

func alreadySetUp(labels []string) string {
	return "Already set up: " + strings.Join(labels, ", ")
}

func (a *App) runMCPUninstall(ctx context.Context, opts *mcpOptions) error {
	sel, err := opts.selection()
	if err != nil {
		return err
	}
	scope, err := a.mcpScope(opts.scope)
	if err != nil {
		return err
	}
	flow, err := a.mcpFlow(opts, sel, "no terminal to choose in: pass --all to remove every "+
		"agnthub MCP install found, or choose with --harness (e.g. --harness claude,codex)")
	if err != nil {
		return err
	}
	ids := []agent.HarnessID(sel)
	if len(ids) == 0 {
		ids = a.supportedHarnesses()
	}
	setup := a.mcpSetup()
	targets, err := setup.Scan(ctx, ids, harness.ScopeGlobal, opts.profiles, "")
	if err != nil {
		return err
	}
	if scope == harness.ScopeProject {
		project, err := setup.Scan(ctx, ids, harness.ScopeProject, nil, "")
		if err != nil {
			return err
		}
		targets = append(targets, project...)
	}
	out := a.printer(opts.json)
	report := mcpReport{Command: "uninstall", Scope: scopeName(scope), URL: mcpsetup.URL}
	var found []*mcpsetup.Target
	for _, t := range targets {
		switch {
		case t.Err != nil:
			report.Warnings = append(report.Warnings, t.Err.Error())
		case t.State.Ours():
			found = append(found, t)
		}
	}
	for _, warning := range report.Warnings {
		fmt.Fprintln(a.Stderr, "warning:", warning)
	}
	if len(found) == 0 {
		return a.emitMCP(out, &report)
	}
	chosen, err := a.chooseMCP(ctx, out, flow, found, mcpQuestions{
		list: "Remove agnthub's MCP server from",
		one:  "Remove agnthub's MCP server from %s?",
		many: "Remove agnthub's MCP server from these harnesses?",
		note: func(t *mcpsetup.Target) string {
			if t.State != mcpsetup.Installed {
				return out.path(t.Path) + " · older agnthub setup"
			}
			return out.path(t.Path)
		},
		preselect: func(*mcpsetup.Target) bool { return true },
	})
	if err != nil {
		return err
	}
	for _, t := range chosen {
		report.add(t, setup.Uninstall(ctx, t))
	}
	return a.emitMCP(out, &report)
}

func scopeName(scope harness.Scope) string {
	if scope == harness.ScopeProject {
		return "project"
	}
	return "user"
}

// mcpReport is the outcome of install or uninstall, printed as a table or JSON.
type mcpReport struct {
	Command     string          `json:"command"`
	Status      string          `json:"status"`
	Scope       string          `json:"scope"`
	URL         string          `json:"url"`
	Results     []mcpResultJSON `json:"results"`
	NotDetected []string        `json:"notDetected,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`

	apiKey bool
	// named: the harnesses were named with --harness or --profile.
	named bool
	// current labels the targets that already had the hosted server;
	// currentShown: they were already named before a prompt.
	current      []string
	currentShown bool
}

type mcpResultJSON struct {
	Harness    string   `json:"harness"`
	Profile    string   `json:"profile,omitempty"`
	Scope      string   `json:"scope"`
	ConfigFile string   `json:"configFile"`
	Before     string   `json:"before"`
	Action     string   `json:"action"`
	Mechanism  string   `json:"mechanism,omitempty"`
	Command    []string `json:"command,omitempty"`
	Backup     string   `json:"backup,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	SignIn     string   `json:"signIn,omitempty"`
}

func (r *mcpReport) add(t *mcpsetup.Target, res mcpsetup.Result) {
	row := mcpResultJSON{
		Harness:    string(t.Harness),
		Profile:    t.Profile,
		Scope:      scopeName(t.Scope),
		ConfigFile: t.Path,
		Before:     string(t.State),
		Action:     string(res.Action),
		Mechanism:  res.Mechanism,
		Command:    res.Command,
		Backup:     res.Backup,
		Reason:     res.Reason,
	}
	if t.Err != nil {
		row.Before = ""
	}
	if !r.apiKey && (res.Action == mcpsetup.ActionInstall || res.Action == mcpsetup.ActionUpdate) {
		row.SignIn = mcpsetup.SignIn(t.Harness, t.Profile)
	}
	r.Results = append(r.Results, row)
}

// Statuses of an MCP report.
const (
	mcpStatusDone    = "done"
	mcpStatusSkipped = "skipped"
	mcpStatusFailed  = "failed"
	mcpStatusNone    = "none"
)

// exit is the report's status and exit code.
func (r *mcpReport) exit() (string, int) {
	if len(r.Results) == 0 {
		return mcpStatusNone, 0
	}
	status, code := mcpStatusDone, 0
	for _, row := range r.Results {
		switch mcpsetup.Action(row.Action) {
		case mcpsetup.ActionFail:
			return mcpStatusFailed, 1
		case mcpsetup.ActionSkip:
			status, code = mcpStatusSkipped, 2
		}
	}
	return status, code
}

func (a *App) emitMCP(out *printer, r *mcpReport) error {
	var code int
	r.Status, code = r.exit()
	if r.Results == nil {
		r.Results = []mcpResultJSON{}
	}
	if out.json {
		if err := out.emitJSON(r); err != nil {
			return err
		}
	} else {
		out.humanMCP(r)
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

func (p *printer) humanMCP(r *mcpReport) {
	s := p.styles
	if r.Status == mcpStatusNone {
		if r.Command == "uninstall" {
			fmt.Fprintln(p.w, "No agnthub MCP installs detected.")
		}
		return
	}
	changed := slices.ContainsFunc(r.Results, func(row mcpResultJSON) bool {
		switch mcpsetup.Action(row.Action) {
		case mcpsetup.ActionInstall, mcpsetup.ActionUpdate, mcpsetup.ActionRemove:
			return true
		}
		return false
	})
	var icon, title string
	switch {
	case r.Status == mcpStatusFailed:
		icon, title = p.emoji("❌"), "agnthub MCP server not "+pastTense(r.Command)+" everywhere"
	case r.Status == mcpStatusSkipped:
		icon, title = p.emoji(
			"📋",
		), "agnthub MCP server "+pastTense(
			r.Command,
		)+", some harnesses skipped"
	case !changed:
		where := "detected"
		if r.named {
			where = "selected"
		}
		fmt.Fprintln(p.w, p.emoji("✅")+s.title.Render(
			"agnthub's MCP server is already set up in every "+where+" harness."))
		p.paragraph(2, s.dim.Render(strings.Join(r.current, ", ")))
		return
	default:
		icon, title = p.emoji("✅"), "agnthub MCP server "+pastTense(r.Command)
	}
	fmt.Fprintln(p.w, icon+s.title.Render(title)+p.dot()+p.scopeTag(r.Scope)+p.dot()+
		s.dim.Render(r.URL))
	p.section()
	t := &table{
		headers: []string{"HARNESS", "PROFILE", "ACTION", "CONFIG FILE"},
		shrink:  3, wrap: -1, indent: p.indent(),
	}
	var notes []string
	backups := ""
	for _, row := range r.Results {
		if row.Action == string(mcpsetup.ActionUnchanged) {
			continue
		}
		t.rows = append(t.rows, []cell{
			{text: row.Harness, style: s.title},
			plainCell(row.Profile),
			p.mcpAction(mcpsetup.Action(row.Action)),
			pathCell(p.path(row.ConfigFile)),
		})
		label := row.Harness
		if row.Profile != "" {
			label += " (profile " + row.Profile + ")"
		}
		if row.Reason != "" {
			notes = append(notes, s.reason.Render("! "+label+": "+row.Reason))
		}
		if row.Backup != "" {
			backups = row.Backup
		}
	}
	p.render(t)
	showCurrent := len(r.current) > 0 && !r.currentShown
	if len(notes) > 0 || backups != "" || showCurrent || len(r.NotDetected) > 0 {
		p.section()
	}
	for _, note := range notes {
		p.paragraph(2, note)
	}
	if backups != "" {
		p.paragraph(2, s.dim.Render("Previous files are backed up in "+p.path(dirOf(backups))+"."))
	}
	if showCurrent {
		p.paragraph(2, s.dim.Render(alreadySetUp(r.current)))
	}
	if len(r.NotDetected) > 0 {
		p.paragraph(2, s.dim.Render("Not detected: "+strings.Join(r.NotDetected, ", ")+
			" (name one with --harness to set it up anyway)."))
	}
	var signIn [][2]string
	for _, row := range r.Results {
		if row.SignIn != "" {
			label := row.Harness
			if row.Profile != "" && row.Profile != "default" {
				label += " " + row.Profile
			}
			signIn = append(signIn, [2]string{label, row.SignIn})
		}
	}
	if len(signIn) > 0 {
		p.section()
		fmt.Fprintln(p.w, p.emoji("🔑")+s.title.Render("Next, sign in with GitHub in each harness"))
		p.fields(signIn)
	}
}

func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i > 0 {
		return path[:i]
	}
	return path
}

func pastTense(command string) string {
	if command == "uninstall" {
		return "removed"
	}
	return "installed"
}

func (p *printer) mcpAction(action mcpsetup.Action) cell {
	s := p.styles
	switch action {
	case mcpsetup.ActionInstall:
		return cell{text: "+ install", style: s.create}
	case mcpsetup.ActionUpdate:
		return cell{text: "~ update", style: s.update}
	case mcpsetup.ActionRemove:
		return cell{text: "- remove", style: s.remove}
	case mcpsetup.ActionSkip:
		return cell{text: "! skipped", style: s.blocked}
	case mcpsetup.ActionFail:
		return cell{text: "! failed", style: s.blocked}
	default:
		return cell{text: "= already installed", style: s.dim}
	}
}

// warnReadable warns when a file now holding the API key is readable by others.
func (a *App) warnReadable(rows []mcpResultJSON) {
	for _, row := range rows {
		if row.Action != string(mcpsetup.ActionInstall) &&
			row.Action != string(mcpsetup.ActionUpdate) {
			continue
		}
		if mode, ok := fileMode(row.ConfigFile); ok && mode&0o077 != 0 {
			fmt.Fprintf(a.Stderr, "warning: %s is readable by other users (mode %04o); "+
				"consider chmod 600\n", row.ConfigFile, mode)
		}
	}
}

func fileMode(path string) (os.FileMode, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Mode().Perm(), true
}
