package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/agnthub-sh/agnthub/internal/apiclient"
	"github.com/agnthub-sh/agnthub/skills"
)

// maxKeyInput bounds what --with-key-stdin reads.
const maxKeyInput = 4 << 10

func (a *App) authCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Sign in to agnthub.sh with an API key for higher rate limits",
		Long: `Manage the agnthub.sh API key the CLI and agnthub mcp send to the registry API.

Everything works without an account. Signed in, searches and registry reads get
higher rate limits, charged to your account instead of your network address:

  tier      anonymous (per address)          with an API key (per account)
  search    60/min, burst 30, 2,000/hour     600/min, burst 120
  detail    300/min, burst 100               3,000/min, burst 500
  install   30/min                           120/min

The key comes from $` + apiclient.KeyVariable + ` when set, else from apiKey in
~/.config/agnthub/config.yaml (written by agnthub auth login, mode 0600). It is
sent only to the registry API ($AGNTHUB_API_URL, apiURL or ` + apiclient.DefaultURL + `),
never to GitHub or any other host. Create, rotate or revoke it at
` + apiclient.KeyPage + `.`,
		Example: `  agnthub auth login
  pbpaste | agnthub auth login --with-key-stdin
  agnthub auth status --json
  agnthub auth logout`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(a.authLoginCommand(ctx), a.authStatusCommand(ctx), a.authLogoutCommand())
	return cmd
}

type authLoginOptions struct {
	noBrowser, withKeyStdin bool
}

func (a *App) authLoginCommand(ctx context.Context) *cobra.Command {
	opts := &authLoginOptions{}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Save an agnthub.sh API key in the user configuration",
		Long: `Open ` + apiclient.KeyPage + ` (sign in with GitHub, then create
or copy your key), paste the key at the hidden prompt, and agnthub checks it with the
registry API and saves it as apiKey in ~/.config/agnthub/config.yaml with mode 0600.

The site shows a key only once, right after you create or rotate it.
--with-key-stdin reads the key from standard input instead, for scripts.`,
		Example: `  agnthub auth login
  agnthub auth login --no-browser
  agnthub auth login --with-key-stdin < key.txt`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runAuthLogin(ctx, opts)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.noBrowser, "no-browser", false, "print the API key page URL only")
	flags.BoolVar(&opts.withKeyStdin, "with-key-stdin", false,
		"read the API key from standard input instead of prompting")
	return cmd
}

func (a *App) authStatusCommand(ctx context.Context) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the API key in use, its account and the current rate limit",
		Long: `Show where the API key comes from ($` + apiclient.KeyVariable + ` or the
configuration), its masked form, the GitHub account it belongs to and the rate limit
the registry API reports for this caller. Without a key it shows the anonymous limit.`,
		Example: `  agnthub auth status
  agnthub auth status --json`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runAuthStatus(ctx, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}

func (a *App) authLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved API key from the user configuration",
		Long: `Remove apiKey from ~/.config/agnthub/config.yaml. agnthub cannot unset
$` + apiclient.KeyVariable + `; while it is set, it keeps being used. To stop a key from
working anywhere, revoke it at ` + apiclient.KeyPage + `.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runAuthLogout()
		},
	}
}

// me is the part of GET /v1/me the CLI reads.
type me struct {
	SignedIn bool   `json:"signed_in"`
	Auth     string `json:"auth"`
	Account  *struct {
		GitHubLogin string  `json:"github_login"`
		Name        *string `json:"name"`
	} `json:"account"`
	APIKey *struct {
		CreatedAt  *string `json:"created_at"`
		LastUsedAt *string `json:"last_used_at"`
	} `json:"api_key"`
}

func (a *App) fetchMe(ctx context.Context, api *apiclient.Config) (*me, *apiclient.Limit, error) {
	out := &me{}
	header, err := apiclient.Get(ctx, apiclient.HTTPClient(a.Env, api), "agnthub/"+Version,
		strings.TrimRight(api.URL, "/")+"/v1/me", out)
	if err != nil {
		return nil, nil, err
	}
	if limit, ok := apiclient.ParseLimit(header, a.Now()); ok {
		return out, &limit, nil
	}
	return out, nil, nil
}

func (a *App) runAuthLogin(ctx context.Context, opts *authLoginOptions) error {
	path := skills.ConfigPath(a.Env)
	configured, err := skills.LoadAPI(path)
	if err != nil {
		return err
	}
	out := a.printer(false)
	var key string
	switch {
	case opts.withKeyStdin:
		raw, err := io.ReadAll(io.LimitReader(a.Stdin, maxKeyInput))
		if err != nil {
			return fmt.Errorf("reading the API key from standard input: %w", err)
		}
		key = string(raw)
	case !a.Interactive:
		return errors.New("no terminal to prompt for the API key: pipe it into " +
			"agnthub auth login --with-key-stdin, or set " + apiclient.KeyVariable)
	default:
		fmt.Fprintln(out.w, out.emoji("🔑")+out.styles.title.Render("Create or copy an API key at ")+
			out.styles.scope.Render(apiclient.KeyPage))
		if !opts.noBrowser && openBrowser(ctx, a.LookPath, apiclient.KeyPage) == nil {
			out.paragraph(0, out.styles.dim.Render("Opened in your browser."))
		}
		out.gapBeforePrompt()
		key, err = a.promptSecret(ctx, "Paste your API key",
			"The site shows a key only once, right after you create or rotate it.")
		if err != nil {
			return err
		}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("no API key given")
	}
	if !apiclient.ValidKey(key) {
		return errors.New("that is not an agnthub API key (keys start with " +
			apiclient.KeyPrefix + "); copy it again from " + apiclient.KeyPage)
	}
	api := apiclient.Config{
		URL:        apiclient.URL(a.Env, configured.URL),
		Credential: apiclient.Credential{Key: key},
	}
	who, _, err := a.fetchMe(ctx, &api)
	if _, rejected := errors.AsType[*apiclient.KeyRejectedError](err); rejected {
		return errors.New("agnthub.sh rejected this API key (revoked or rotated?); " +
			"copy the current one from " + apiclient.KeyPage)
	}
	if err != nil {
		return err
	}
	if !who.SignedIn || who.Auth != "api_key" || who.Account == nil {
		return fmt.Errorf("the registry API (%s) did not sign this key in as an agnthub API key; "+
			"nothing was saved", api.URL)
	}
	if _, err := skills.SetAPIKey(path, key); err != nil {
		return err
	}
	s := out.styles
	fmt.Fprintln(out.w, out.emoji("✅")+s.title.Render("Signed in as "+who.Account.GitHubLogin)+
		out.dot()+s.dim.Render("key ")+apiclient.Mask(key)+out.dot()+
		s.dim.Render("saved to ")+out.path(path))
	if env := strings.TrimSpace(a.Env.Get(apiclient.KeyVariable)); env != "" && env != key {
		out.paragraph(0, s.reduced.Render(apiclient.KeyVariable+
			" is set and wins over the saved key; unset it to use this one."))
	}
	return nil
}

// promptSecret asks for a value without echoing it.
func (a *App) promptSecret(ctx context.Context, title, description string) (string, error) {
	prompter, ok := a.Prompter.(interface {
		Secret(ctx context.Context, title, description string) (string, error)
	})
	if !ok {
		prompter = &formPrompter{in: a.Stdin, out: a.Stdout}
	}
	return prompter.Secret(ctx, title, description)
}

// openBrowser starts the platform's URL opener without waiting for it.
func openBrowser(ctx context.Context, lookPath func(string) (string, error), target string) error {
	name, args := "xdg-open", []string{target}
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	}
	executable, err := lookPath(name)
	if err != nil {
		return err
	}
	//nolint:gosec // G204: the platform's fixed URL opener with the constant key page URL
	cmd := exec.CommandContext(context.WithoutCancel(ctx), executable, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// authStatus is agnthub auth status --json and the auth_status MCP tool's result.
type authStatus struct {
	// SignedIn reports a key the API accepted.
	SignedIn bool `json:"signed_in"`
	// Auth is api_key when the API accepted the key, else anonymous.
	Auth string `json:"auth"`
	// Source is where the key comes from: env, config, or "" without a key.
	Source        string         `json:"source,omitempty"`
	Key           string         `json:"key,omitempty"`
	GitHubLogin   string         `json:"github_login,omitempty"`
	Name          string         `json:"name,omitempty"`
	KeyCreatedAt  string         `json:"key_created_at,omitempty"`
	KeyLastUsedAt string         `json:"key_last_used_at,omitempty"`
	APIURL        string         `json:"api_url"`
	Config        string         `json:"config"`
	RateLimit     *rateLimitJSON `json:"rate_limit,omitempty"`
}

type rateLimitJSON struct {
	Resource      string `json:"resource"`
	Limit         int64  `json:"limit"`
	Remaining     int64  `json:"remaining"`
	WindowSeconds int64  `json:"window_seconds,omitempty"`
	ResetSeconds  *int64 `json:"reset_seconds,omitempty"`
}

func (a *App) authStatus(ctx context.Context, api *apiclient.Config) (*authStatus, error) {
	if api.Err != nil {
		return nil, api.Err
	}
	status := &authStatus{
		Auth:   "anonymous",
		Source: string(api.Credential.Source),
		APIURL: api.URL,
		Config: skills.ConfigPath(a.Env),
	}
	if api.Credential.Key != "" {
		status.Key = apiclient.Mask(api.Credential.Key)
	}
	who, limit, err := a.fetchMe(ctx, api)
	if err != nil {
		return nil, err
	}
	if who.SignedIn && who.Auth == "api_key" {
		status.SignedIn, status.Auth = true, who.Auth
	}
	if status.SignedIn && who.Account != nil {
		status.GitHubLogin = who.Account.GitHubLogin
		if who.Account.Name != nil {
			status.Name = *who.Account.Name
		}
	}
	if status.SignedIn && who.APIKey != nil {
		status.KeyCreatedAt = deref(who.APIKey.CreatedAt)
		status.KeyLastUsedAt = deref(who.APIKey.LastUsedAt)
	}
	if limit != nil {
		status.RateLimit = &rateLimitJSON{
			Resource: limit.Resource, Limit: limit.Limit, Remaining: limit.Remaining,
			WindowSeconds: int64(limit.Window / time.Second),
		}
		if limit.Reset >= 0 {
			reset := int64((limit.Reset + time.Second - 1) / time.Second)
			status.RateLimit.ResetSeconds = &reset
		}
	}
	return status, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (a *App) runAuthStatus(ctx context.Context, asJSON bool) error {
	configured, err := skills.LoadAPI(skills.ConfigPath(a.Env))
	if err != nil {
		return err
	}
	api := apiclient.Load(a.Env, configured.URL, configured.Key)
	status, err := a.authStatus(ctx, &api)
	if err != nil {
		return err
	}
	out := a.printer(asJSON)
	if asJSON {
		return out.emitJSON(status)
	}
	s := out.styles
	switch {
	case status.SignedIn:
		title := "Signed in as " + status.GitHubLogin
		if status.Name != "" && status.Name != status.GitHubLogin {
			title += " (" + status.Name + ")"
		}
		fmt.Fprintln(out.w, out.emoji("✅")+s.title.Render(title))
	case status.Key != "":
		fmt.Fprintln(out.w, out.emoji("❌")+s.blocked.Render("API key not accepted")+
			out.dot()+s.dim.Render("anonymous limits apply"))
	default:
		fmt.Fprintln(out.w, out.emoji("📋")+s.title.Render("Not signed in")+out.dot()+
			s.dim.Render("anonymous limits apply"))
	}
	var rows [][2]string
	if status.Key != "" {
		from := "from " + apiclient.KeyVariable
		if status.Source == string(apiclient.SourceConfig) {
			from = "from " + out.path(status.Config)
		}
		rows = append(rows, [2]string{"key", status.Key + " " + s.dim.Render(from)})
	}
	if status.KeyLastUsedAt != "" {
		rows = append(rows, [2]string{"last used", status.KeyLastUsedAt})
	}
	rows = append(rows, [2]string{"api", status.APIURL})
	if limit := status.RateLimit; limit != nil {
		text := limit.Resource + " " + strconv.FormatInt(limit.Remaining, 10) + " of " +
			strconv.FormatInt(limit.Limit, 10) + " left"
		if limit.WindowSeconds > 0 {
			text += " per " + strconv.FormatInt(limit.WindowSeconds, 10) + " s"
		}
		if limit.ResetSeconds != nil {
			text += s.dim.Render(", resets in " + strconv.FormatInt(*limit.ResetSeconds, 10) + " s")
		}
		rows = append(rows, [2]string{"limit", text})
	}
	out.fields(rows)
	if !status.SignedIn {
		out.section()
		out.paragraph(0, s.dim.Render("Sign in with `agnthub auth login` for higher limits."))
	}
	return nil
}

func (a *App) runAuthLogout() error {
	path := skills.ConfigPath(a.Env)
	removed, err := skills.SetAPIKey(path, "")
	if err != nil {
		return err
	}
	out := a.printer(false)
	s := out.styles
	if removed {
		fmt.Fprintln(out.w, out.emoji("✅")+s.title.Render("Signed out")+out.dot()+
			s.dim.Render("removed the API key from ")+out.path(path))
	} else {
		fmt.Fprintln(out.w, out.emoji("📋")+s.title.Render("No API key saved")+out.dot()+
			out.path(path))
	}
	if strings.TrimSpace(a.Env.Get(apiclient.KeyVariable)) != "" {
		out.paragraph(0, s.reduced.Render(apiclient.KeyVariable+
			" is still set, and agnthub keeps using it: agnthub cannot change your "+
			"environment, so unset it yourself."))
	}
	return nil
}

// fields prints label/value lines at the content indent with the labels aligned.
func (p *printer) fields(rows [][2]string) {
	width := 0
	for _, row := range rows {
		width = max(width, len(row[0]))
	}
	pad := strings.Repeat(" ", p.indent())
	for _, row := range rows {
		label := row[0] + strings.Repeat(" ", width-len(row[0]))
		fmt.Fprintln(p.w, pad+p.styles.dim.Render(label)+"  "+row[1])
	}
}

func (a *App) addAuthStatusTool(server *mcp.Server, api *apiclient.Config) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "auth_status",
		Title: "agnthub API key and rate limit (agnthub auth status)",
		Description: "Shows whether this server sends an agnthub.sh API key (and from where), " +
			"the GitHub account it belongs to and the rate limit the registry API reports, " +
			"like `agnthub auth status --json`. Without a key, anonymous limits apply; the " +
			"user can sign in with `agnthub auth login` for higher ones.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (
		*mcp.CallToolResult, *authStatus, error,
	) {
		status, err := a.authStatus(ctx, api)
		return nil, status, err
	})
}
