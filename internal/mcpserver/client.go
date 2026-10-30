package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

// Bounds on one registry API call.
const (
	requestTimeout = 20 * time.Second
	maxResponse    = 8 << 20
)

// Client reads the public registry API.
type Client struct {
	// BaseURL is the API origin, e.g. https://api.agnthub.sh.
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
}

// APIError is a registry API refusal, reported to the agent as a tool error.
type APIError struct {
	Status int
	Detail string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusNotFound:
		return "not found on agnthub: " + e.Detail
	case http.StatusTooManyRequests:
		return "the agnthub API is rate limiting this client; try again in a minute"
	case http.StatusServiceUnavailable:
		return "the agnthub API is temporarily unavailable: " + e.Detail
	}
	return fmt.Sprintf("agnthub API error %d: %s", e.Status, e.Detail)
}

// SearchQuery is GET /v1/search.
type SearchQuery struct {
	Text      string
	Harnesses []string
	Sort      string
	Page      int
	PerPage   int
}

// Search runs a full-text search over eligible Agents.
func (c *Client) Search(ctx context.Context, q *SearchQuery) (*SearchPage, error) {
	values := url.Values{}
	if q.Text != "" {
		values.Set("q", q.Text)
	}
	for _, h := range q.Harnesses {
		values.Add("harness", h)
	}
	if q.Sort != "" {
		values.Set("sort", q.Sort)
	}
	if q.Page > 0 {
		values.Set("page", strconv.Itoa(q.Page))
	}
	if q.PerPage > 0 {
		values.Set("per_page", strconv.Itoa(q.PerPage))
	}
	page := &SearchPage{}
	return page, c.get(ctx, "/v1/search", values, page)
}

// Repository reads a listed repository with its Agents.
func (c *Client) Repository(ctx context.Context, owner, repo string) (*Repository, error) {
	out := &Repository{}
	return out, c.get(ctx, repositoryPath(owner, repo), nil, out)
}

// Agent reads one Agent of a listed repository.
func (c *Client) Agent(ctx context.Context, owner, repo, id string) (*AgentDetail, error) {
	out := &AgentDetail{}
	return out, c.get(ctx, repositoryPath(owner, repo)+"/agents/"+url.PathEscape(id), nil, out)
}

func repositoryPath(owner, repo string) string {
	return "/v1/repositories/github/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// problem is the RFC 9457 body of an API refusal.
type problem struct {
	Detail     string `json:"detail"`
	Title      string `json:"title"`
	Withdrawal *struct {
		Reason string `json:"reason"`
	} `json:"withdrawal"`
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	target := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("agnthub API unreachable at %s: %w", c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("reading the agnthub API response: %w", err)
	}
	if len(body) > maxResponse {
		return errors.New("the agnthub API response is too large")
	}
	if resp.StatusCode != http.StatusOK {
		return apiError(resp.StatusCode, body)
	}
	if err := sonic.Unmarshal(body, out); err != nil {
		return fmt.Errorf("unexpected agnthub API response: %w", err)
	}
	return nil
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
