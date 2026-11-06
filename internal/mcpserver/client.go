package mcpserver

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agnthub-sh/agnthub/internal/apiclient"
)

// Client reads the public registry API. Refusals are apiclient errors, so rate limits and
// rejected API keys read the same here as on the command line.
type Client struct {
	// BaseURL is the API origin, e.g. https://api.agnthub.sh.
	BaseURL string
	// HTTP carries the user's API key to BaseURL (see apiclient.HTTPClient).
	HTTP      *http.Client
	UserAgent string
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

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	target := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	_, err := apiclient.Get(ctx, c.HTTP, c.UserAgent, target, out)
	return err
}
