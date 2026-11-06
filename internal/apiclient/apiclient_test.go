package apiclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agnthub-sh/agnthub/internal/apiclient"
	"github.com/agnthub-sh/agnthub/platform/env"
)

const key = "agh_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLM"

// recorder is a server that remembers the Authorization header of every request.
type recorder struct {
	*httptest.Server
	seen     atomic.Pointer[string]
	requests atomic.Int64
}

func newRecorder(t *testing.T, handler http.HandlerFunc) *recorder {
	t.Helper()
	r := &recorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		auth := req.Header.Get("Authorization")
		r.seen.Store(&auth)
		if handler != nil {
			handler(w, req)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) authorization() string {
	if p := r.seen.Load(); p != nil {
		return *p
	}
	return ""
}

func get(t *testing.T, client *http.Client, target string) error {
	t.Helper()
	var out map[string]any
	_, err := apiclient.Get(context.Background(), client, "test", target, &out)
	return err
}

func TestKeyGoesToTheAPIOriginOnly(t *testing.T) {
	other := newRecorder(t, nil)
	api := newRecorder(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/redirect" {
			http.Redirect(w, req, other.URL+"/landed", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	cfg := apiclient.Config{URL: api.URL, Credential: apiclient.Credential{Key: key}}
	client := apiclient.HTTPClient(env.Env{}, &cfg)

	if err := get(t, client, api.URL+"/v1/me"); err != nil {
		t.Fatal(err)
	}
	if got := api.authorization(); got != "Bearer "+key {
		t.Fatalf("API saw Authorization %q", got)
	}
	// Another host (here another port of the same address) never sees the key, neither
	// directly nor through a redirect from the API.
	if err := get(t, client, other.URL+"/x"); err != nil {
		t.Fatal(err)
	}
	if err := get(t, client, api.URL+"/redirect"); err != nil {
		t.Fatal(err)
	}
	if other.requests.Load() != 2 || other.authorization() != "" {
		t.Fatalf("another host saw Authorization %q", other.authorization())
	}
}

func TestEnvBeatsConfigAndMalformedKeysNeverGoOut(t *testing.T) {
	const configured = "agh_configured"
	e := env.FromMap(map[string]string{apiclient.KeyVariable: key})
	if c, err := apiclient.Resolve(e, configured); err != nil || c.Key != key ||
		c.Source != apiclient.SourceEnv {
		t.Fatalf("env must win: %+v %v", c, err)
	}
	if c, err := apiclient.Resolve(env.Env{}, configured); err != nil || c.Key != configured ||
		c.Source != apiclient.SourceConfig {
		t.Fatalf("config without env: %+v %v", c, err)
	}
	if c, err := apiclient.Resolve(env.Env{}, ""); err != nil || c.Key != "" {
		t.Fatalf("no key is anonymous: %+v %v", c, err)
	}

	api := newRecorder(t, nil)
	for _, bad := range []string{"ghp_token", "agh_", "agh_with space", "Bearer agh_x"} {
		cfg := apiclient.Load(env.FromMap(map[string]string{apiclient.KeyVariable: bad}),
			api.URL, "")
		if _, malformed := errors.AsType[*apiclient.MalformedKeyError](cfg.Err); !malformed {
			t.Fatalf("%q must be refused locally, got %v", bad, cfg.Err)
		}
		err := get(t, apiclient.HTTPClient(env.Env{}, &cfg), api.URL+"/v1/me")
		if _, malformed := errors.AsType[*apiclient.MalformedKeyError](err); !malformed {
			t.Fatalf("%q: want the local refusal, got %v", bad, err)
		}
	}
	if n := api.requests.Load(); n != 0 {
		t.Fatalf("a malformed key must not fall back to anonymous requests: %d sent", n)
	}
}

func TestShortRateLimitIsRetriedOnceLongOneFails(t *testing.T) {
	for name, tc := range map[string]struct {
		retryAfter string
		key        string
		wantErr    bool
		wantHint   bool
		requests   int64
	}{
		"short is waited out":    {retryAfter: "0", requests: 2},
		"long fails anonymously": {retryAfter: "30", wantErr: true, wantHint: true, requests: 1},
		"long fails with a key":  {retryAfter: "30", key: key, wantErr: true, requests: 1},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			api := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", tc.retryAfter)
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = w.Write([]byte(`{}`))
			})
			cfg := apiclient.Config{URL: api.URL, Credential: apiclient.Credential{Key: tc.key}}
			err := get(t, apiclient.HTTPClient(env.Env{}, &cfg), api.URL+"/v1/search")
			if n := api.requests.Load(); n != tc.requests {
				t.Fatalf("%d requests, want %d", n, tc.requests)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			limited, ok := errors.AsType[*apiclient.RateLimitError](err)
			if !ok || limited.RetryAfter != 30*time.Second {
				t.Fatalf("want a 30 s rate-limit error, got %v", err)
			}
			if hint := strings.Contains(err.Error(), "agnthub auth login"); hint != tc.wantHint {
				t.Fatalf("hint shown = %v, want %v: %s", hint, tc.wantHint, err)
			}
		})
	}
}

func TestRejectedKeyIsNeverRetriedAnonymously(t *testing.T) {
	api := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":401,"detail":"invalid API key"}`))
	})
	cfg := apiclient.Config{URL: api.URL, Credential: apiclient.Credential{Key: key}}
	err := get(t, apiclient.HTTPClient(env.Env{}, &cfg), api.URL+"/v1/me")
	if _, rejected := errors.AsType[*apiclient.KeyRejectedError](err); !rejected {
		t.Fatalf("want the rejected-key error, got %v", err)
	}
	if !strings.Contains(err.Error(), "agnthub auth login") ||
		!strings.Contains(err.Error(), apiclient.KeyVariable) {
		t.Fatalf("the error must say how to fix it: %s", err)
	}
	if n := api.requests.Load(); n != 1 {
		t.Fatalf("%d requests: a rejected key must not be retried", n)
	}
}

func TestParseLimitReadsIETFAndGitHubStyleHeaders(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	ietf := http.Header{}
	ietf.Set("RateLimit-Policy", `"search";q=600;w=60`)
	ietf.Set("RateLimit", `"search";r=412;t=17`)
	got, ok := apiclient.ParseLimit(ietf, now)
	want := apiclient.Limit{
		Resource:  "search",
		Limit:     600,
		Remaining: 412,
		Window:    time.Minute,
		Reset:     17 * time.Second,
	}
	if !ok || got != want {
		t.Fatalf("IETF headers: %+v %v", got, ok)
	}
	github := http.Header{}
	github.Set("X-RateLimit-Limit", "300")
	github.Set("X-RateLimit-Remaining", "299")
	github.Set("X-RateLimit-Reset", "1000005")
	github.Set("X-RateLimit-Resource", "detail")
	got, ok = apiclient.ParseLimit(github, now)
	want = apiclient.Limit{Resource: "detail", Limit: 300, Remaining: 299, Reset: 5 * time.Second}
	if !ok || got != want {
		t.Fatalf("X-RateLimit headers: %+v %v", got, ok)
	}
	if _, ok := apiclient.ParseLimit(http.Header{}, now); ok {
		t.Fatal("no headers, no limit")
	}
	if d := apiclient.RetryAfter(ietf, now); d != 17*time.Second {
		t.Fatalf("a 429 without Retry-After waits for the reset, got %s", d)
	}
}
