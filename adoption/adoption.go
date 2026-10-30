// Package adoption reports successful installs from public GitHub repositories to the
// agnthub registry, which counts reported adoption for Agents it already lists; a report
// never adds a repository to the registry. Reporting is best-effort: it never changes what
// was installed or the command's result, it honors the combined DO_NOT_TRACK/DISABLE_TELEMETRY
// opt-out before anything else, and it sends only a public owner/repo, the installed
// revision and, per Agent, its id, origin, the Harness ids that received artifacts and an
// agent-scoped random identity.
package adoption

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"golang.org/x/net/http/httpproxy"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/platform/env"
	"github.com/heynemann/agnthub/source"
	"github.com/heynemann/agnthub/state"
)

// DefaultAPIURL is the production registry API.
const DefaultAPIURL = "https://api.agnthub.sh"

// DefaultGitHubURL is where anonymous public-visibility checks go.
const DefaultGitHubURL = "https://" + source.GitHubHost

// Timeout bounds all reporting work after one install, checks and requests together.
const Timeout = 3 * time.Second

// OptOutVariables disable install reporting when any is non-empty.
var OptOutVariables = []string{"DO_NOT_TRACK", "DISABLE_TELEMETRY"}

// OptedOut reports the combined sharing opt-out.
func OptedOut(e env.Env) bool {
	for _, key := range OptOutVariables {
		if e.Get(key) != "" {
			return true
		}
	}
	return false
}

// ChildEnv is the environment for child installers: when the user opted out, every opt-out
// variable is set so the child's own telemetry is disabled too, whichever one it reads.
func ChildEnv(e env.Env) env.Env {
	if !OptedOut(e) {
		return e
	}
	for _, key := range OptOutVariables {
		if e.Get(key) == "" {
			e = e.With(key, "1")
		}
	}
	return e
}

// Install is one artifact an install created or replaced.
type Install struct {
	Source  state.LockedSource
	Agent   string
	Origin  state.Origin
	Harness agent.HarnessID
}

// Reporter sends install reports. Zero-value fields take production defaults in New.
type Reporter struct {
	Env env.Env
	// APIURL is the registry API origin.
	APIURL string
	// GitHubURL is the origin of anonymous public-visibility checks.
	GitHubURL string
	Client    *http.Client
	UserAgent string
	Timeout   time.Duration
}

// New returns a reporter for e that sends to APIURL(e, configured). Requests honor the proxy
// variables of e.
func New(e env.Env, version, configured string) *Reporter {
	return &Reporter{
		Env:       e,
		APIURL:    APIURL(e, configured),
		GitHubURL: DefaultGitHubURL,
		Client:    HTTPClient(e),
		UserAgent: "agnthub/" + version,
		Timeout:   Timeout,
	}
}

// APIURL is the registry API origin: AGNTHUB_API_URL, else configured (the user
// configuration's apiURL), else DefaultAPIURL.
func APIURL(e env.Env, configured string) string {
	if override := e.Get("AGNTHUB_API_URL"); override != "" {
		return override
	}
	if configured != "" {
		return configured
	}
	return DefaultAPIURL
}

// HTTPClient returns a client whose requests honor the proxy variables of e.
func HTTPClient(e env.Env) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxy := proxyConfig(e).ProxyFunc()
	transport.Proxy = func(req *http.Request) (*url.URL, error) { return proxy(req.URL) }
	return &http.Client{Transport: transport}
}

func proxyConfig(e env.Env) *httpproxy.Config {
	get := func(names ...string) string {
		for _, name := range names {
			if v := e.Get(name); v != "" {
				return v
			}
		}
		return ""
	}
	return &httpproxy.Config{
		HTTPProxy:  get("HTTP_PROXY", "http_proxy"),
		HTTPSProxy: get("HTTPS_PROXY", "https_proxy"),
		NoProxy:    get("NO_PROXY", "no_proxy"),
	}
}

// report is the POST /v1/installs body.
type report struct {
	Owner    string          `json:"owner"`
	Repo     string          `json:"repo"`
	Revision string          `json:"revision"`
	Agents   []reportedAgent `json:"agents"`
}

type reportedAgent struct {
	Agent          string   `json:"agent"`
	InstallationID string   `json:"installation_id"`
	Origin         string   `json:"origin"`
	Harnesses      []string `json:"harnesses"`
}

// group is one GitHub source's report under construction.
type group struct {
	owner, repo, revision string
	agents                []reportedAgent
}

// Report sends what installs placed, grouped per GitHub source, and ignores every failure.
// Local and other-host sources are never sent; a GitHub source is sent only after an
// anonymous check shows it is public, so private repository names never leave the machine.
func (r *Reporter) Report(ctx context.Context, installs []Install) {
	if OptedOut(r.Env) || r.APIURL == "" || r.Client == nil {
		return
	}
	groups := groupInstalls(installs)
	if len(groups) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	public := groups[:0]
	for _, g := range groups {
		if r.public(ctx, g.owner, g.repo) {
			public = append(public, g)
		}
	}
	if len(public) == 0 {
		return
	}
	ids, err := state.LoadIdentities(state.IdentitiesPath(r.Env.StateHome()))
	if err != nil {
		return
	}
	for gi := range public {
		g := &public[gi]
		key := source.Locator{Kind: source.KindGitHub, Owner: g.owner, Repo: g.repo}.Key()
		for ai := range g.agents {
			id, err := ids.For(state.IdentityKey(key, g.agents[ai].Agent))
			if err != nil {
				return
			}
			g.agents[ai].InstallationID = id
		}
	}
	if err := ids.Save(); err != nil {
		return
	}
	for _, g := range public {
		r.post(ctx, report{Owner: g.owner, Repo: g.repo, Revision: g.revision, Agents: g.agents})
	}
}

// groupInstalls keeps GitHub sources with a resolved revision and merges each Agent's
// artifacts: it is direct when any artifact was installed directly.
func groupInstalls(installs []Install) []group {
	var groups []group
	for _, in := range installs {
		if in.Source.Kind != string(source.KindGitHub) || in.Source.Revision == "" {
			continue
		}
		owner, repo, ok := strings.Cut(in.Source.Locator, "/")
		if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
			continue
		}
		gi := slices.IndexFunc(groups, func(g group) bool {
			return strings.EqualFold(g.owner, owner) && strings.EqualFold(g.repo, repo) &&
				g.revision == in.Source.Revision
		})
		if gi < 0 {
			groups = append(groups, group{owner: owner, repo: repo, revision: in.Source.Revision})
			gi = len(groups) - 1
		}
		g := &groups[gi]
		ai := slices.IndexFunc(g.agents, func(a reportedAgent) bool { return a.Agent == in.Agent })
		if ai < 0 {
			g.agents = append(g.agents, reportedAgent{
				Agent: in.Agent, Origin: string(state.OriginDependency), Harnesses: []string{},
			})
			ai = len(g.agents) - 1
		}
		a := &g.agents[ai]
		if in.Origin == state.OriginDirect {
			a.Origin = string(state.OriginDirect)
		}
		if !slices.Contains(a.Harnesses, string(in.Harness)) {
			a.Harnesses = append(a.Harnesses, string(in.Harness))
		}
	}
	return groups
}

// public checks anonymously, with no credentials of any kind, whether GitHub serves the
// repository to everyone. Any doubt counts as not public.
func (r *Reporter) public(ctx context.Context, owner, repo string) bool {
	u := strings.TrimSuffix(r.GitHubURL, "/") + "/" + url.PathEscape(owner) + "/" +
		url.PathEscape(repo) + ".git/info/refs?service=git-upload-pack"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", r.UserAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return false
	}
	defer drain(resp)
	return resp.StatusCode == http.StatusOK
}

func (r *Reporter) post(ctx context.Context, body report) {
	payload, err := sonic.Marshal(body)
	if err != nil {
		return
	}
	u := strings.TrimSuffix(r.APIURL, "/") + "/v1/installs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", r.UserAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return
	}
	drain(resp)
}

// drain reads a bounded amount so the connection can be reused, then closes the body.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}
