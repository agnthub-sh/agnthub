package apiclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
)

// Bounds on one registry API call.
const (
	requestTimeout = 20 * time.Second
	maxResponse    = 8 << 20
)

// APIError is any other registry API refusal.
type APIError struct {
	Status int
	Detail string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusNotFound:
		return "not found on agnthub: " + e.Detail
	case http.StatusServiceUnavailable:
		return "the agnthub API is temporarily unavailable: " + e.Detail
	}
	return fmt.Sprintf("agnthub API error %d: %s", e.Status, e.Detail)
}

// problem is the RFC 9457 body of an API refusal.
type problem struct {
	Detail     string `json:"detail"`
	Title      string `json:"title"`
	Withdrawal *struct {
		Reason string `json:"reason"`
	} `json:"withdrawal"`
}

// Get reads target with client and decodes a 200 JSON body into out, returning the
// response headers. Short rate limits are waited out once (see Do); refusals are a
// MalformedKeyError, KeyRejectedError, RateLimitError or APIError.
func Get(ctx context.Context, client *http.Client, userAgent, target string, out any) (
	http.Header, error,
) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := Do(client, req)
	if err != nil {
		if malformed, ok := errors.AsType[*MalformedKeyError](err); ok {
			return nil, malformed
		}
		return nil, fmt.Errorf("agnthub API unreachable at %s://%s: %w",
			req.URL.Scheme, req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := Check(resp); err != nil {
		return resp.Header, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return resp.Header, fmt.Errorf("reading the agnthub API response: %w", err)
	}
	if len(body) > maxResponse {
		return resp.Header, errors.New("the agnthub API response is too large")
	}
	if resp.StatusCode != http.StatusOK {
		return resp.Header, apiError(resp.StatusCode, body)
	}
	if err := sonic.Unmarshal(body, out); err != nil {
		return resp.Header, fmt.Errorf("unexpected agnthub API response: %w", err)
	}
	return resp.Header, nil
}

func apiError(status int, body []byte) error {
	var p problem
	_ = sonic.Unmarshal(body, &p)
	detail := p.Detail
	if detail == "" {
		detail = p.Title
	}
	if detail == "" {
		detail = http.StatusText(status)
	}
	if p.Withdrawal != nil {
		detail += " (withdrawn: " + p.Withdrawal.Reason + ")"
	}
	return &APIError{Status: status, Detail: detail}
}
