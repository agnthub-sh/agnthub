package apiclient

import (
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxRetryWait is the longest Retry-After Do waits out before its one retry.
const MaxRetryWait = 10 * time.Second

// defaultRetryAfter stands in for a 429 that says nothing about when to come back.
const defaultRetryAfter = time.Minute

// RateLimitError is a 429 from the registry API.
type RateLimitError struct {
	// RetryAfter is how long the API asked the client to wait.
	RetryAfter time.Duration
	// Anonymous reports a request sent without an API key: signing in raises the limit.
	Anonymous bool
}

func (e *RateLimitError) Error() string {
	seconds := max(int64(math.Ceil(e.RetryAfter.Seconds())), 1)
	msg := "agnthub.sh rate limit reached; try again in " + strconv.FormatInt(seconds, 10) + " s"
	if e.Anonymous {
		msg += " (sign in with `agnthub auth login` for higher limits)"
	}
	return msg
}

// KeyRejectedError is a 401 for a request that carried an API key: the key is malformed,
// revoked or rotated. It is never retried anonymously.
type KeyRejectedError struct{}

func (*KeyRejectedError) Error() string {
	return "Your agnthub API key was rejected (revoked or rotated?). " +
		"Run `agnthub auth login` or unset " + KeyVariable + "."
}

// Check returns the error for a credential or rate-limit refusal: KeyRejectedError for a
// 401 to a request that carried a key, RateLimitError for a 429, nil otherwise.
func Check(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		if KeySent(resp) {
			return &KeyRejectedError{}
		}
	case http.StatusTooManyRequests:
		return &RateLimitError{
			RetryAfter: RetryAfter(resp.Header, time.Now()),
			Anonymous:  !KeySent(resp),
		}
	}
	return nil
}

// Do sends req through client. A 429 whose Retry-After is at most MaxRetryWait is waited
// out once and the request sent again; a longer one, or a second 429, is returned as is.
// A request with a body is resent only when it has GetBody.
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	//nolint:gosec // G704: callers build requests for the configured registry API origin
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusTooManyRequests {
		return resp, err
	}
	wait := RetryAfter(resp.Header, time.Now())
	if wait > MaxRetryWait || (req.Body != nil && req.Body != http.NoBody && req.GetBody == nil) {
		return resp, nil
	}
	drain(resp)
	if err := sleep(req.Context(), wait); err != nil {
		return nil, err
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	return client.Do(retry) //nolint:gosec // G704: the same request, sent again
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// drain reads a bounded amount so the connection can be reused, then closes the body.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// RetryAfter is how long a 429 asks the client to wait: Retry-After in seconds or as an
// HTTP date, else the RateLimit reset, else X-RateLimit-Reset, else a minute.
func RetryAfter(h http.Header, now time.Time) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if seconds, err := strconv.ParseInt(v, 10, 64); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if at, err := http.ParseTime(v); err == nil {
			return max(at.Sub(now), 0)
		}
	}
	if limit, ok := ParseLimit(h, now); ok && limit.Reset >= 0 {
		return limit.Reset
	}
	return defaultRetryAfter
}

// Limit is one rate-limit tier's state as the API reported it.
type Limit struct {
	// Resource is the tier, e.g. search or detail.
	Resource  string
	Limit     int64
	Remaining int64
	// Window is the policy's window; 0 when the API did not say.
	Window time.Duration
	// Reset is the time until the allowance is full again; -1 when the API did not say.
	Reset time.Duration
}

// ParseLimit reads the IETF RateLimit and RateLimit-Policy headers, falling back to the
// X-RateLimit-* headers. ok is false when the response carries neither.
func ParseLimit(h http.Header, now time.Time) (Limit, bool) {
	limit := Limit{Reset: -1}
	policyName, policy := parseStructured(h.Get("RateLimit-Policy"))
	stateName, state := parseStructured(h.Get("RateLimit"))
	found := false
	if q, ok := policy["q"]; ok {
		limit.Resource, limit.Limit, found = policyName, q, true
		if w, ok := policy["w"]; ok {
			limit.Window = time.Duration(w) * time.Second
		}
	}
	if r, ok := state["r"]; ok && (!found || stateName == policyName) {
		limit.Resource, limit.Remaining = stateName, r
		if t, ok := state["t"]; ok {
			limit.Reset = time.Duration(t) * time.Second
		}
		if found {
			return limit, true
		}
	}
	xLimit, errLimit := strconv.ParseInt(h.Get("X-RateLimit-Limit"), 10, 64)
	xRemaining, errRemaining := strconv.ParseInt(h.Get("X-RateLimit-Remaining"), 10, 64)
	if errLimit != nil || errRemaining != nil {
		return limit, false
	}
	limit.Limit, limit.Remaining = xLimit, xRemaining
	if resource := h.Get("X-RateLimit-Resource"); resource != "" {
		limit.Resource = resource
	}
	if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		limit.Reset = max(time.Unix(reset, 0).Sub(now), 0)
	}
	return limit, true
}

// parseStructured reads the first item of a RateLimit-style header, `"name";k=v;k=v`, with
// its integer parameters.
func parseStructured(header string) (string, map[string]int64) {
	if header == "" {
		return "", nil
	}
	item, _, _ := strings.Cut(header, ",")
	parts := strings.Split(item, ";")
	name := strings.Trim(strings.TrimSpace(parts[0]), `"`)
	params := make(map[string]int64, len(parts)-1)
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			params[key] = n
		}
	}
	return name, params
}
