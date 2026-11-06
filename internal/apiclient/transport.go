package apiclient

import (
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"

	"github.com/agnthub-sh/agnthub/platform/env"
)

// HTTPClient returns a client whose requests honor the proxy variables of e and carry
// cfg's credential to cfg.URL's origin only. Requests to any other origin, GitHub
// included, go out exactly as built. When cfg.Err is set, requests to the API fail with
// it instead of going out anonymously.
func HTTPClient(e env.Env, cfg *Config) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	proxy := proxyConfig(e).ProxyFunc()
	base.Proxy = func(req *http.Request) (*url.URL, error) { return proxy(req.URL) }
	return &http.Client{Transport: NewTransport(base, cfg)}
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

// Transport adds `Authorization: Bearer <key>` to requests for one API origin.
type Transport struct {
	base http.RoundTripper
	// scheme, host and port are the API origin, lowercased, with the scheme's default port.
	scheme, host, port string
	// authorization is the precomputed header value, "" when anonymous.
	authorization string
	err           error
}

// NewTransport wraps base. An unparsable or empty cfg.URL authenticates nothing.
func NewTransport(base http.RoundTripper, cfg *Config) *Transport {
	t := &Transport{base: base, err: cfg.Err}
	if cfg.Credential.Key != "" {
		t.authorization = "Bearer " + cfg.Credential.Key
	}
	if u, err := url.Parse(cfg.URL); err == nil && u.Host != "" {
		t.scheme = strings.ToLower(u.Scheme)
		t.host = strings.ToLower(u.Hostname())
		t.port = portOf(u)
	}
	return t
}

// RoundTrip authenticates requests for the API origin; it never modifies req. A redirect
// to another origin is a new round trip and goes out without the key.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if (t.authorization == "" && t.err == nil) || !t.forAPI(req.URL) {
		return t.base.RoundTrip(req)
	}
	if t.err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, t.err
	}
	authed := req.Clone(req.Context())
	authed.Header.Set("Authorization", t.authorization)
	resp, err := t.base.RoundTrip(authed)
	if resp != nil {
		resp.Request = authed
	}
	return resp, err
}

func (t *Transport) forAPI(u *url.URL) bool {
	return t.host != "" && strings.EqualFold(u.Scheme, t.scheme) &&
		strings.EqualFold(u.Hostname(), t.host) && portOf(u) == t.port
}

func portOf(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "http") {
		return "80"
	}
	return "443"
}

// KeySent reports whether resp answers a request that carried an API key.
func KeySent(resp *http.Response) bool {
	return resp.Request != nil && strings.HasPrefix(resp.Request.Header.Get("Authorization"),
		"Bearer "+KeyPrefix)
}
