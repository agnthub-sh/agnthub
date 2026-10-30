package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/heynemann/agnthub/adoption"
	"github.com/heynemann/agnthub/internal/cli"
	"github.com/heynemann/agnthub/platform/env"
	"github.com/heynemann/agnthub/source"
	"github.com/heynemann/agnthub/state"
)

const toolsRevision = "1111111111111111111111111111111111111111"

// Registry API behaviours the fake can simulate.
const (
	apiAccept int32 = iota
	apiFail
	apiHang
)

// registryFake stands in for the registry API and GitHub's anonymous clone endpoint.
type registryFake struct {
	api     *httptest.Server
	github  *httptest.Server
	reports chan string
	checks  atomic.Int64
	mode    atomic.Int32
	public  map[string]bool
}

func newRegistryFake(t *testing.T, public ...string) *registryFake {
	t.Helper()
	f := &registryFake{reports: make(chan string, 32), public: map[string]bool{}}
	for _, repo := range public {
		f.public[repo] = true
	}
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/installs" {
			t.Errorf("unexpected registry request %s %s", r.Method, r.URL.Path)
		}
		f.reports <- string(body)
		switch f.mode.Load() {
		case apiFail:
			w.WriteHeader(http.StatusInternalServerError)
		case apiHang:
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	f.github = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.checks.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("visibility checks must be anonymous")
		}
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".git/info/refs")
		if !f.public[repo] || r.URL.Query().Get("service") != "git-upload-pack" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(f.api.Close)
	t.Cleanup(f.github.Close)
	return f
}

// taken returns the report bodies received so far.
func (f *registryFake) taken() []string {
	var out []string
	for {
		select {
		case body := <-f.reports:
			out = append(out, body)
		default:
			return out
		}
	}
}

func (h *harnessT) runReporting(
	f *registryFake,
	git fakeGit,
	extraEnv map[string]string,
	args ...string,
) (int, string) {
	h.t.Helper()
	values := map[string]string{"HOME": h.home, "AGNTHUB_API_URL": f.api.URL}
	for key, value := range extraEnv {
		values[key] = value
	}
	e := env.FromMap(values)
	reporter := adoption.New(e, "test", "")
	reporter.GitHubURL = f.github.URL
	if timeout, ok := extraEnv["TEST_REPORT_TIMEOUT"]; ok {
		d, err := time.ParseDuration(timeout)
		if err != nil {
			h.t.Fatal(err)
		}
		reporter.Timeout = d
	}
	var out bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:      e,
		WorkDir:  h.project,
		Stdin:    strings.NewReader(""),
		Stdout:   &out,
		Stderr:   &out,
		Now:      func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Opener:   source.Opener{Git: git},
		Adoption: reporter,
	})
	return app.Run(context.Background(), args), out.String()
}

type reportJSON struct {
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Revision string `json:"revision"`
	Agents   []struct {
		Agent          string   `json:"agent"`
		InstallationID string   `json:"installation_id"`
		Origin         string   `json:"origin"`
		Harnesses      []string `json:"harnesses"`
	} `json:"agents"`
}

func parseReport(t *testing.T, body string) reportJSON {
	t.Helper()
	var r reportJSON
	if err := sonic.UnmarshalString(body, &r); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return r
}

var identityPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (h *harnessT) mustSucceed(code int, out string) {
	h.t.Helper()
	if code != 0 {
		h.t.Fatalf("exit %d: %s", code, out)
	}
}

// ompProfiles creates omp profiles, which agnthub never initializes itself.
func (h *harnessT) ompProfiles(names ...string) {
	h.t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(h.home, ".omp", "profiles", name), 0o750); err != nil {
			h.t.Fatal(err)
		}
	}
}

func TestPublicInstallReportsOnlyAgentsOriginsAndPlacedHarnesses(t *testing.T) {
	h := newHarness(t)
	h.writeReviewerSource(reviewerManifest)
	h.ompProfiles("work")
	f := newRegistryFake(t, "acme/tools")
	git := fakeGit{"acme/tools#v1": toolsRepo(toolsRevision)}

	// The direct Agent comes from a local (private) source; its GitHub Dependencies are
	// reported as Dependencies without any trace of the parent.
	h.mustSucceed(h.runReporting(f, git, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "--harness", "omp", "--global", "--extra", "omp.profile=work",
		"--yes"))

	bodies := f.taken()
	if len(bodies) != 1 {
		t.Fatalf("want one report for acme/tools, got %q", bodies)
	}
	for _, private := range []string{
		h.home, h.project, h.source, "reviewer", "planner",
		"work", "profile", "agents/",
	} {
		if strings.Contains(bodies[0], private) {
			t.Fatalf("report leaks %q: %s", private, bodies[0])
		}
	}
	r := parseReport(t, bodies[0])
	if r.Owner != "acme" || r.Repo != "tools" || r.Revision != toolsRevision || len(r.Agents) != 2 {
		t.Fatalf("report %+v", r)
	}
	ids := map[string]string{}
	for _, a := range r.Agents {
		if a.Origin != "dependency" || strings.Join(a.Harnesses, ",") != "claude,omp" ||
			!identityPattern.MatchString(a.InstallationID) {
			t.Fatalf("agent %+v", a)
		}
		ids[a.Agent] = a.InstallationID
	}
	if ids["linter"] == "" || ids["linter"] == ids["style-guide"] {
		t.Fatalf("each Agent needs its own identity: %v", ids)
	}
	// Only the GitHub source was checked, anonymously; the local one never left the machine.
	if n := f.checks.Load(); n != 1 {
		t.Fatalf("%d visibility checks, want 1", n)
	}
	// Identities live in user state, never in a shareable lock.
	lock, err := os.ReadFile(state.LockPath(h.project, filepath.Join(h.home, ".config"), true))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(lock), ids["linter"]) {
		t.Fatal("the lock must not carry installation identities")
	}
	stored, err := state.LoadIdentities(
		state.IdentitiesPath(filepath.Join(h.home, ".local", "state")))
	if err != nil || stored.IDs["github.com/acme/tools::linter"] != ids["linter"] {
		t.Fatalf("stored identities %+v, %v", stored, err)
	}
}

func TestReinstallsProfilesAndUpdatesReuseTheAgentIdentity(t *testing.T) {
	h := newHarness(t)
	h.ompProfiles("work", "other")
	f := newRegistryFake(t, "acme/tools")
	git := fakeGit{"acme/tools": toolsRepo(toolsRevision)}
	add := func(extra ...string) {
		t.Helper()
		args := append([]string{"add", "acme/tools", "--agent", "linter", "--global", "--yes"},
			extra...)
		h.mustSucceed(h.runReporting(f, git, nil, args...))
	}

	add("--harness", "omp", "--extra", "omp.profile=work")
	first := f.taken()
	if len(first) != 1 {
		t.Fatalf("reports %q", first)
	}
	linter := parseReport(t, first[0]).Agents[0]
	if linter.Agent != "linter" || linter.Origin != "direct" {
		t.Fatalf("agent %+v", linter)
	}

	// A reinstall writes nothing, so it reports nothing.
	add("--harness", "omp", "--extra", "omp.profile=work")
	if again := f.taken(); len(again) != 0 {
		t.Fatalf("a no-op reinstall reported %q", again)
	}

	// Another omp profile and another Harness keep the same identity: the registry counts
	// them as one logical install with one omp and one claude adoption.
	add("--harness", "omp", "--extra", "omp.profile=other")
	add("--harness", "claude")
	for i, body := range f.taken() {
		r := parseReport(t, body)
		for _, a := range r.Agents {
			if a.Agent == "linter" && a.InstallationID != linter.InstallationID {
				t.Fatalf("report %d changed the identity: %+v", i, a)
			}
		}
		if strings.Contains(body, "other") {
			t.Fatalf("report %d leaks the profile: %s", i, body)
		}
	}

	// An update that replaces content reports the new revision under the same identity.
	updated := strings.Repeat("2", 40)
	git["acme/tools"] = toolsRepo(updated)
	h.mustSucceed(h.runReporting(f, git, nil, "update", "--global", "--yes"))
	reports := f.taken()
	if len(reports) != 1 {
		t.Fatalf("update reports %q", reports)
	}
	r := parseReport(t, reports[0])
	if r.Revision != updated {
		t.Fatalf("update report %+v", r)
	}
	for _, a := range r.Agents {
		if a.Agent == "linter" && a.InstallationID != linter.InstallationID {
			t.Fatalf("update changed the identity: %+v", a)
		}
	}
}

func TestSharingOptOutSendsNothingAndCreatesNoIdentity(t *testing.T) {
	for _, key := range adoption.OptOutVariables {
		t.Run(key, func(t *testing.T) {
			h := newHarness(t)
			f := newRegistryFake(t, "acme/tools")
			git := fakeGit{"acme/tools": toolsRepo(toolsRevision)}

			h.mustSucceed(h.runReporting(f, git, map[string]string{key: "1"},
				"add", "acme/tools", "--agent", "linter", "--harness", "claude", "--yes"))

			if bodies, n := f.taken(), f.checks.Load(); len(bodies) != 0 || n != 0 {
				t.Fatalf("opted out, yet %d checks and reports %q", n, bodies)
			}
			if _, err := os.Stat(
				state.IdentitiesPath(filepath.Join(h.home, ".local", "state")),
			); !os.IsNotExist(
				err,
			) {
				t.Fatalf("opt-out must not create an identity: %v", err)
			}
		})
	}
}

func TestPrivateLocalAndOtherHostSourcesAreNeverReported(t *testing.T) {
	h := newHarness(t)
	h.writeReviewerSource(strings.Replace(reviewerManifest,
		"        - agent: linter\n          source: acme/tools#v1\n", "", 1))
	f := newRegistryFake(t) // nothing is public
	other, err := source.Parse("https://git.example.com/acme/tools.git", h.project, env.Env{})
	if err != nil {
		t.Fatal(err)
	}
	git := fakeGit{
		"acme/private":  toolsRepo(toolsRevision),
		other.Display(): toolsRepo(toolsRevision),
	}

	h.mustSucceed(h.runReporting(f, git, nil,
		"add", "acme/private", "--agent", "linter", "--harness", "claude", "--yes"))
	if n := f.checks.Load(); n != 1 {
		t.Fatalf("%d visibility checks for one GitHub source", n)
	}
	h.mustSucceed(h.runReporting(f, git, nil,
		"add", "https://git.example.com/acme/tools.git", "--agent", "linter",
		"--harness", "codex", "--yes"))
	h.mustSucceed(h.runReporting(f, git, nil,
		"add", h.source, "--agent", "reviewer", "--harness", "opencode", "--yes"))

	if bodies := f.taken(); len(bodies) != 0 {
		t.Fatalf("non-public sources were reported: %q", bodies)
	}
	if n := f.checks.Load(); n != 1 {
		t.Fatalf("local and other-host sources must not even be checked: %d checks", n)
	}
}

func TestRegistryOutageNeverAffectsTheInstall(t *testing.T) {
	for name, mode := range map[string]int32{"failing": apiFail, "hanging": apiHang} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			f := newRegistryFake(t, "acme/tools")
			f.mode.Store(mode)
			git := fakeGit{"acme/tools": toolsRepo(toolsRevision)}

			start := time.Now()
			h.mustSucceed(h.runReporting(f, git, map[string]string{"TEST_REPORT_TIMEOUT": "300ms"},
				"add", "acme/tools", "--agent", "linter", "--harness", "claude", "--yes"))
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("reporting held the install for %s", elapsed)
			}
			if len(f.taken()) != 1 {
				t.Fatal("the report should have been attempted")
			}
			if _, err := os.Stat(
				filepath.Join(h.project, ".claude", "agents", "linter.md"),
			); err != nil {
				t.Fatalf("install must succeed whatever the registry does: %v", err)
			}
		})
	}

	h := newHarness(t)
	f := newRegistryFake(t, "acme/tools")
	f.api.Close() // unreachable registry
	h.mustSucceed(h.runReporting(f, fakeGit{"acme/tools": toolsRepo(toolsRevision)}, nil,
		"add", "acme/tools", "--agent", "linter", "--harness", "claude", "--yes"))
}

func TestSharingOptOutReachesTheSkillInstaller(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"opted out":   {map[string]string{"DISABLE_TELEMETRY": "yes"}, "1 yes"},
		"not opt out": {nil, " "},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.writeSkillAgent(boundsV1)
			f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
			envFile := filepath.Join(filepath.Dir(h.home), "backend-env")
			writeFile(t, f.backend, fakeBackendScript+
				`printf '%s %s\n' "$DO_NOT_TRACK" "$DISABLE_TELEMETRY" > "`+envFile+`"`+"\n")
			values := map[string]string{"FAKE_SRC": f.src, "FAKE_ARGS": f.args}
			for k, v := range tc.env {
				values[k] = v
			}
			h.mustSucceed(h.runWith(f.git(), values,
				"add", h.source, "--agent", "reviewer", "--harness", "claude", "--yes"))
			got, err := os.ReadFile(envFile)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSuffix(string(got), "\n") != tc.want {
				t.Fatalf("backend saw DO_NOT_TRACK/DISABLE_TELEMETRY %q, want %q", got, tc.want)
			}
		})
	}
}
