package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/internal/apiclient"
	"github.com/agnthub-sh/agnthub/internal/cli"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/skills"
)

// authAPI fakes GET /v1/me: testKey signs in as octocat, no key is anonymous and any
// other key is refused like the registry API refuses it.
type authAPI struct {
	*httptest.Server
	requests atomic.Int64
}

func newAuthAPI(t *testing.T) *authAPI {
	t.Helper()
	a := &authAPI{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if auth != "" && auth != "Bearer "+testKey {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":401,"detail":"invalid API key"}`))
			return
		}
		limit, remaining := "300", "299"
		if auth != "" {
			limit, remaining = "3000", "2999"
		}
		w.Header().Set("RateLimit-Policy", `"detail";q=`+limit+`;w=60`)
		w.Header().Set("RateLimit", `"detail";r=`+remaining+`;t=1`)
		if auth == "" {
			_, _ = w.Write([]byte(`{"signed_in":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"signed_in":true,"auth":"api_key",` +
			`"account":{"github_id":1,"github_login":"octocat","name":"The Octocat"},` +
			`"api_key":{"prefix":"agh_","last4":"JKLM","created_at":"2026-11-06T10:00:00Z",` +
			`"last_used_at":"2026-11-06T11:00:00Z"}}`))
	}))
	t.Cleanup(a.Close)
	return a
}

func (h *harnessT) configPath() string {
	return filepath.Join(h.home, ".config", "agnthub", "config.yaml")
}

func (h *harnessT) runAuth(values map[string]string, stdin string, args ...string) (int, string) {
	h.t.Helper()
	e := env.FromMap(map[string]string{"HOME": h.home})
	for key, value := range values {
		e = e.With(key, value)
	}
	var out bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:      e,
		WorkDir:  h.project,
		Stdin:    strings.NewReader(stdin),
		Stdout:   &out,
		Stderr:   &out,
		Now:      func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Adoption: offline,
	})
	return app.Run(context.Background(), args), out.String()
}

func TestAuthLoginWithStdinSavesTheKey0600AndLogoutRemovesIt(t *testing.T) {
	h := newHarness(t)
	api := newAuthAPI(t)
	writeFile(t, h.configPath(), "# my settings\napiURL: "+api.URL+"\n")

	code, out := h.runAuth(nil, testKey+"\n", "auth", "login", "--with-key-stdin")
	if code != 0 || !strings.Contains(out, "Signed in as octocat") {
		t.Fatalf("exit %d: %s", code, out)
	}
	info, err := os.Stat(h.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config written with mode %o, want 600", perm)
	}
	raw, err := os.ReadFile(h.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# my settings") {
		t.Fatalf("login must keep the rest of the configuration:\n%s", raw)
	}
	api2, err := skills.LoadAPI(h.configPath())
	if err != nil || api2.Key != testKey || api2.URL != api.URL {
		t.Fatalf("saved configuration %+v, %v", api2, err)
	}
	if strings.Contains(out, testKey) {
		t.Fatal("login must not print the key")
	}

	// Logout removes the key; an environment key stays in use, and logout says so.
	code, out = h.runAuth(map[string]string{apiclient.KeyVariable: testKey}, "", "auth", "logout")
	if code != 0 || !strings.Contains(out, apiclient.KeyVariable) {
		t.Fatalf("exit %d: %s", code, out)
	}
	api2, err = skills.LoadAPI(h.configPath())
	if err != nil || api2.Key != "" || api2.URL != api.URL {
		t.Fatalf("after logout %+v, %v", api2, err)
	}
}

func TestAuthLoginRefusesKeysWithoutSaving(t *testing.T) {
	h := newHarness(t)
	api := newAuthAPI(t)
	values := map[string]string{apiclient.URLVariable: api.URL}

	// Not an agnthub key: refused before any request.
	code, out := h.runAuth(values, "ghp_github_token\n", "auth", "login", "--with-key-stdin")
	if code == 0 || api.requests.Load() != 0 {
		t.Fatalf("a non-agnthub key must be refused locally: exit %d, %d requests: %s",
			code, api.requests.Load(), out)
	}
	// Revoked or rotated: the API refuses it.
	code, out = h.runAuth(values, "agh_revoked\n", "auth", "login", "--with-key-stdin")
	if code == 0 || !strings.Contains(out, "rejected") {
		t.Fatalf("exit %d: %s", code, out)
	}
	// No terminal and no --with-key-stdin: nothing to read the key from.
	if code, out = h.runAuth(values, "", "auth", "login"); code == 0 {
		t.Fatalf("login without a terminal must fail: %s", out)
	}
	if _, err := os.Stat(h.configPath()); !os.IsNotExist(err) {
		t.Fatalf("a refused key must not be saved: %v", err)
	}
}

type statusJSON struct {
	SignedIn    bool   `json:"signed_in"`
	Auth        string `json:"auth"`
	Source      string `json:"source"`
	Key         string `json:"key"`
	GitHubLogin string `json:"github_login"`
	RateLimit   *struct {
		Resource  string `json:"resource"`
		Limit     int64  `json:"limit"`
		Remaining int64  `json:"remaining"`
	} `json:"rate_limit"`
}

func TestAuthStatusJSON(t *testing.T) {
	h := newHarness(t)
	api := newAuthAPI(t)
	values := map[string]string{apiclient.URLVariable: api.URL}

	status := func(values map[string]string) statusJSON {
		t.Helper()
		code, out := h.runAuth(values, "", "auth", "status", "--json")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		var s statusJSON
		if err := sonic.UnmarshalString(out, &s); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return s
	}

	anonymous := status(values)
	if anonymous.SignedIn || anonymous.Auth != "anonymous" || anonymous.Key != "" ||
		anonymous.RateLimit == nil || anonymous.RateLimit.Limit != 300 {
		t.Fatalf("anonymous status %+v", anonymous)
	}

	writeFile(t, h.configPath(), "apiKey: "+testKey+"\n")
	signedIn := status(values)
	if !signedIn.SignedIn || signedIn.Auth != "api_key" || signedIn.Source != "config" ||
		signedIn.Key != "agh_…JKLM" || signedIn.GitHubLogin != "octocat" ||
		signedIn.RateLimit == nil || signedIn.RateLimit.Resource != "detail" ||
		signedIn.RateLimit.Limit != 3000 || signedIn.RateLimit.Remaining != 2999 {
		t.Fatalf("signed-in status %+v", signedIn)
	}

	// The environment wins over the configuration; a rejected key is an error, never
	// an anonymous status.
	code, out := h.runAuth(map[string]string{
		apiclient.URLVariable: api.URL, apiclient.KeyVariable: "agh_rotated",
	}, "", "auth", "status", "--json")
	if code == 0 || !strings.Contains(out, "API key was rejected") {
		t.Fatalf("exit %d: %s", code, out)
	}
}
