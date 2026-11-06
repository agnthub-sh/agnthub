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
  agnthub mcp install --harness claude,codex --yes
  agnthub mcp install --harness omp --profile work --yes
  agnthub mcp uninstall --yes`,
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
	yes       bool
	json      bool
}

func (o *mcpOptions) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringSliceVarP(&o.harnesses, "harness", "H", nil,
		"harnesses, comma-separated or repeated: "+harnessList()+", or all")
	flags.StringArrayVar(&o.profiles, "profile", nil,
		"omp profile (repeatable; \"default\" for the default profile); implies --harness omp")
	flags.StringVar(
		&o.scope,
		"scope",
		"user",
		"user, or project for the current project's configuration",
	)
	flags.BoolVarP(&o.yes, "yes", "y", false, "do not prompt")
	flags.BoolVar(&o.json, "json", false, "print the result as JSON (never prompts)")
}

func harnessList() string {
	names := make([]string, 0, len(agent.AllHarnesses))
	for _, id := range agent.AllHarnesses {
		names = append(names, string(id))
	}
	return strings.Join(names, ", ")
}

const mcpExitCodes = `

Exit status: 0 when every selected harness is set up (or already was); 1 on an
error (invalid flags, a cancelled prompt, no --harness without a terminal, or a
harness whose configuration could not be changed; the others are still done); 2
when a harness was skipped because its agnthub entry was not set up by agnthub
(rerun with --force to replace it).`

func (a *App) mcpInstallCommand(ctx context.Context) *cobra.Command {
	opts := &mcpOptions{}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Add agnthub's hosted MCP server to your harnesses",
		Long: `Add agnthub's hosted MCP server (` + mcpsetup.URL + `) to your harnesses.

In a terminal, install lists the harnesses detected on this machine (and every omp
profile) with what each has now; the ones without agnthub are preselected (space
toggles, ctrl+a toggles all, enter confirms). Without a terminal, or with --yes or
--json, choose with --harness (and --profile for omp profiles); --harness all
selects every detected harness.

Each harness gets an "agnthub" server (Streamable HTTP, ` + mcpsetup.URL + `) in its
user configuration, or with --scope project in the current project's. Older agnthub
entries (the former /mcp URLs, or the local server ` + "`npx agnthub-cli mcp`" + `) are
replaced; an agnthub entry agnthub did not write is kept unless you confirm or pass
--force. Claude Code, Copilot CLI and Gemini CLI are configured with their own mcp
command when it is on PATH; the others (and every --api-key or project-scope setup
those commands cannot do) by editing the file: only the agnthub entry changes, the
previous file is copied to ~/.local/state/agnthub/mcp-backups, and the new one is
written atomically with the old file mode (0600 for a new file). A configuration
that does not parse is never changed.

You then sign in with GitHub in each harness (install prints how). --api-key writes
your agnthub API key (` + apiclient.KeyVariable + `, or the one ` + "`agnthub auth login`" + ` saved) as an
Authorization header instead: the key is then stored in plain text in each
harness's configuration file.` + mcpExitCodes,
		Example: `  agnthub mcp install
  agnthub mcp install --harness claude,codex --yes
  agnthub mcp install --harness all --yes --json
  agnthub mcp install --harness omp --profile default --profile work --yes
  agnthub mcp install --harness cursor --scope project --yes
  agnthub mcp install --harness claude --api-key --yes`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runMCPInstall(ctx, opts)
		},
	}
	opts.register(cmd)
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
or the local server ` + "`npx agnthub-cli mcp`" + `). In a terminal it lists the ones it found,
all selected; --yes removes every one found, --harness and --profile narrow the
search. Removal uses the harness's own mcp command where install would, otherwise
the same careful file edit with a backup. Other servers, and an agnthub entry
agnthub did not write, are never removed.

Exit status: 0 when every selected entry was removed, or none was found; 1 on an
error (invalid flags, a cancelled prompt, no --yes or --harness without a terminal,
or a configuration that could not be changed).`,
		Example: `  agnthub mcp uninstall
  agnthub mcp uninstall --yes
  agnthub mcp uninstall --harness omp --profile work --yes
  agnthub mcp uninstall --scope project --yes --json`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runMCPUninstall(ctx, opts)
		},
	}
	opts.register(cmd)
	return cmd
}

// mcpSelection is what the flags select.
type mcpSelection struct {
	// ids are the named harnesses; nil when none was named.
	ids []agent.HarnessID
	// all: --harness all.
	all bool
}

func (o *mcpOptions) selection() (mcpSelection, error) {
	var sel mcpSelection
	for _, name := range o.harnesses {
		name = strings.TrimSpace(name)
		if name == allSelector || name == allHarnesses {
			sel.all = true
			continue
		}
		id, err := agent.ParseHarness(name)
		if err != nil {
			return sel, err
		}
		if !slices.Contains(sel.ids, id) {
			sel.ids = append(sel.ids, id)
		}
	}
	if sel.all && len(sel.ids) > 0 {
		return sel, errors.New("--harness all already selects every harness; drop the other values")
	}
	if len(o.profiles) > 0 {
		switch {
		case len(o.harnesses) == 0:
			sel.ids = []agent.HarnessID{agent.OMP}
		case !sel.all && !slices.Contains(sel.ids, agent.OMP):
			return sel, errors.New("--profile selects omp profiles; add --harness omp")
		}
	}
	return sel, nil
}

func (sel mcpSelection) named() bool { return sel.all || len(sel.ids) > 0 }

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
	interactive := a.Interactive && !opts.yes && !opts.json
	if !sel.named() && !interactive {
		return errors.New("choose harnesses with --harness (e.g. --harness claude,codex, or " +
			"--harness all for every detected harness); run it in a terminal to pick them from a list")
	}
	ids, undetected := sel.ids, []agent.HarnessID(nil)
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
	selected := targets
	force := opts.force
	if interactive && !sel.named() {
		if selected, err = a.promptMCPInstall(ctx, out, targets); err != nil {
			return err
		}
		if !force && slices.ContainsFunc(selected, func(t *mcpsetup.Target) bool {
			return t.State == mcpsetup.Foreign
		}) {
			if force, err = a.confirmReplace(ctx, out, selected); err != nil {
				return err
			}
		}
	}
	if apiKey != "" {
		fmt.Fprintln(
			a.Stderr,
			"warning: --api-key stores your agnthub API key in plain text in each "+
				"selected harness's MCP configuration; anyone who can read those files can use it",
		)
	}
	report := mcpReport{
		Command: "install",
		Scope:   scopeName(scope),
		URL:     mcpsetup.URL,
		apiKey:  apiKey != "",
	}
	for _, t := range selected {
		res := setup.Install(ctx, t, force)
		report.add(t, res)
	}
	for _, t := range targets {
		if !slices.Contains(selected, t) && t.State == mcpsetup.Installed {
			report.add(t, mcpsetup.Result{Action: mcpsetup.ActionUnchanged})
		}
	}
	for _, id := range undetected {
		report.NotDetected = append(report.NotDetected, string(id))
	}
	if apiKey != "" {
		a.warnReadable(report.Results)
	}
	return a.emitMCP(out, &report)
}

// promptMCPInstall offers every target: the ones without agnthub preselected,
// the others marked with what they have.
func (a *App) promptMCPInstall(
	ctx context.Context,
	out *printer,
	targets []*mcpsetup.Target,
) ([]*mcpsetup.Target, error) {
	if !slices.ContainsFunc(
		targets,
		func(t *mcpsetup.Target) bool { return t.State != mcpsetup.Installed },
	) {
		return nil, nil
	}
	options := make([]Option, 0, len(targets))
	for i, t := range targets {
		label := a.targetLabel(t)
		switch {
		case t.Err != nil:
			label += " (cannot read its configuration)"
		case t.State == mcpsetup.Installed:
			label += " (already installed)"
		case t.State == mcpsetup.Outdated,
			t.State == mcpsetup.Legacy,
			t.State == mcpsetup.LegacyStdio:
			label += " (older agnthub setup; select to update)"
		case t.State == mcpsetup.Foreign:
			label += " (has another agnthub server: " + t.Existing + "; select to replace)"
		}
		options = append(options, Option{
			Value:    strconv.Itoa(i),
			Label:    label,
			Selected: t.Err == nil && t.State == mcpsetup.Absent,
		})
	}
	out.gapBeforePrompt()
	values, err := a.Prompter.MultiSelect(ctx, "Harnesses to set up agnthub's MCP server in",
		"Space toggles, ctrl+a toggles all, enter confirms.", options)
	if err != nil {
		return nil, err
	}
	selected := make([]*mcpsetup.Target, 0, len(values))
	for _, value := range values {
		i, err := strconv.Atoi(value)
		if err != nil || i < 0 || i >= len(targets) {
			return nil, fmt.Errorf("unexpected choice %q", value)
		}
		selected = append(selected, targets[i])
	}
	if len(selected) == 0 {
		return nil, errCancelled
	}
	return selected, nil
}

// confirmReplace asks before replacing agnthub entries agnthub did not write.
func (a *App) confirmReplace(
	ctx context.Context,
	out *printer,
	selected []*mcpsetup.Target,
) (bool, error) {
	var lines []string
	for _, t := range selected {
		if t.State == mcpsetup.Foreign {
			lines = append(lines, a.targetLabel(t)+": "+t.Existing+" in "+out.path(t.Path))
		}
	}
	out.gapBeforePrompt()
	ok, err := a.Prompter.Confirm(ctx, "Replace these agnthub servers agnthub did not set up?",
		strings.Join(lines, "\n"))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errCancelled
	}
	return true, nil
}

func (a *App) targetLabel(t *mcpsetup.Target) string {
	label := fmt.Sprintf("%-9s %s", t.Harness, a.harnessName(t.Harness))
	if t.Profile != "" {
		label += " · profile " + t.Profile
	}
	return label
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
	interactive := a.Interactive && !opts.yes && !opts.json
	if !sel.named() && !interactive && !opts.yes {
		return errors.New("pass --yes to remove every agnthub MCP install found, or choose " +
			"harnesses with --harness; run it in a terminal to pick them from a list")
	}
	ids := sel.ids
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
	selected := found
	if interactive && !sel.named() {
		options := make([]Option, 0, len(found))
		for i, t := range found {
			label := a.targetLabel(t) + "  " + out.path(t.Path)
			if t.State != mcpsetup.Installed {
				label += " (older agnthub setup)"
			}
			options = append(options, Option{Value: strconv.Itoa(i), Label: label, Selected: true})
		}
		out.gapBeforePrompt()
		values, err := a.Prompter.MultiSelect(ctx, "Remove agnthub's MCP server from",
			"Space toggles, ctrl+a toggles all, enter confirms.", options)
		if err != nil {
			return err
		}
		selected = selected[:0:0]
		for _, value := range values {
			i, err := strconv.Atoi(value)
			if err != nil || i < 0 || i >= len(found) {
				return fmt.Errorf("unexpected choice %q", value)
			}
			selected = append(selected, found[i])
		}
	}
	for _, t := range selected {
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
		icon, title = p.emoji("✅"), "agnthub MCP server already installed"
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
	if len(notes) > 0 || backups != "" || len(r.NotDetected) > 0 {
		p.section()
	}
	for _, note := range notes {
		p.paragraph(2, note)
	}
	if backups != "" {
		p.paragraph(2, s.dim.Render("Previous files are backed up in "+p.path(dirOf(backups))+"."))
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
