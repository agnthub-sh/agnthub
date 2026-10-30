package mcpserver

import "time"

// The types below decode the public registry API (/v1) and are also the tools' structured
// output. They hold the fields an agent needs to choose and install Agents; the API sends
// more, which decoding ignores. JSON names follow the API so outputs read like its docs.
// Slices are omitempty because the tools' output schema does not allow null arrays.

// RepositoryRef names a GitHub repository.
type RepositoryRef struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

// License is a repository's detected license and the content policy it implies.
type License struct {
	SPDXID *string `json:"spdx_id,omitempty"`
	Name   *string `json:"name,omitempty"`
	Policy string  `json:"policy"            jsonschema:"licensed: full instructions are published; metadata_only: only metadata and links"`
	Reason string  `json:"reason"`
}

// GitHubStats are GitHub's own numbers; a missing value is unknown, never zero.
type GitHubStats struct {
	Stars        *int       `json:"stars,omitempty"`
	Watchers     *int       `json:"watchers,omitempty"`
	Contributors *int       `json:"contributors,omitempty"`
	PushedAt     *time.Time `json:"pushed_at,omitempty"`
}

// Adoption counts reported installations (deduplicated CLI identities, not people).
type Adoption struct {
	Total      int               `json:"total"`
	Direct     int               `json:"direct"`
	Dependency int               `json:"dependency"`
	Last7Days  int               `json:"last_7_days"`
	Last30Days int               `json:"last_30_days"`
	Harnesses  []HarnessAdoption `json:"harnesses,omitempty"`
}

// HarnessAdoption is the reported installations for one Harness.
type HarnessAdoption struct {
	Harness  string `json:"harness"`
	Installs int    `json:"installs"`
}

// SearchHarness is a Harness an Agent installs into.
type SearchHarness struct {
	Harness string `json:"harness"`
	Status  string `json:"status"  jsonschema:"native: an authored Variant; generated: converted from the portable definition"`
}

// SnippetSegment is a run of matched or unmatched snippet text.
type SnippetSegment struct {
	Text  string `json:"text"`
	Match bool   `json:"match"`
}

// Snippet is plain text around the search match.
type Snippet struct {
	Field    string           `json:"field"`
	Segments []SnippetSegment `json:"segments,omitempty"`
}

// SearchResult is one eligible Agent.
type SearchResult struct {
	Agent                 string          `json:"agent"`
	Description           *string         `json:"description,omitempty"`
	Repository            RepositoryRef   `json:"repository"`
	RepositoryDescription *string         `json:"repository_description,omitempty"`
	Harnesses             []SearchHarness `json:"harnesses,omitempty"`
	Portable              bool            `json:"portable"`
	License               License         `json:"license"`
	IndexedCommit         string          `json:"indexed_commit"`
	IndexedAt             time.Time       `json:"indexed_at"`
	Snippet               *Snippet        `json:"snippet,omitempty"`
	Adoption              Adoption        `json:"adoption"`
	AgnthubStars          int             `json:"agnthub_stars"`
}

// SearchPage is one page of search results.
type SearchPage struct {
	Items   []SearchResult `json:"items,omitempty"`
	Page    int            `json:"page"`
	PerPage int            `json:"per_page"`
	Total   int            `json:"total"           jsonschema:"matches before the eligibility re-check, capped at 10000; an upper bound"`
	HasNext bool           `json:"has_next"`
	Sort    string         `json:"sort"`
}

// RepositoryInfo describes a listed repository and its indexed snapshot.
type RepositoryInfo struct {
	FullName      string      `json:"full_name"`
	Owner         string      `json:"owner"`
	Name          string      `json:"name"`
	Description   *string     `json:"description,omitempty"`
	HTMLURL       string      `json:"html_url"`
	DefaultBranch string      `json:"default_branch"`
	IndexedCommit string      `json:"indexed_commit"`
	IndexedAt     time.Time   `json:"indexed_at"`
	RefreshedAt   time.Time   `json:"refreshed_at"`
	CommitURL     string      `json:"commit_url,omitempty"`
	License       License     `json:"license"`
	AgentCount    int         `json:"agent_count"`
	Harnesses     []string    `json:"harnesses,omitempty"`
	GitHubStats   GitHubStats `json:"github_stats"`
	Fork          bool        `json:"fork"`
	Manifest      bool        `json:"manifest"`
}

// Repository is a listed repository with its Agents.
type Repository struct {
	RepositoryInfo

	Agents         []Agent       `json:"agents,omitempty"`
	Problems       []ScanProblem `json:"problems,omitempty"`
	RefreshProblem *string       `json:"refresh_problem,omitempty"`
	Adoption       Adoption      `json:"adoption"`
	AgnthubStars   int           `json:"agnthub_stars"`
	// AgentsOmitted counts Agents left out to bound the output.
	AgentsOmitted int `json:"agents_omitted,omitempty"`
}

// SourceFile is a definition file at the indexed commit.
type SourceFile struct {
	Path      string  `json:"path"`
	SourceURL string  `json:"source_url"`
	Body      *string `json:"body,omitempty" jsonschema:"full instructions; only under the licensed policy and when requested"`
	// BodyTruncated reports a body cut to bound the output.
	BodyTruncated bool `json:"body_truncated,omitempty"`
}

// Variant is a Harness-native definition authored in the repository.
type Variant struct {
	SourceFile

	Harness      string        `json:"harness"`
	Dependencies *Dependencies `json:"dependencies,omitempty"`
}

// Dependencies are the Agents and Skills an Agent requires.
type Dependencies struct {
	Agents []AgentDependency `json:"agents,omitempty"`
	Skills []SkillDependency `json:"skills,omitempty"`
}

// AgentDependency is a required Agent, from the same repository unless Source is set.
type AgentDependency struct {
	Agent  string  `json:"agent"`
	Source *string `json:"source,omitempty"`
}

// SkillDependency is a required Skill.
type SkillDependency struct {
	Source     string  `json:"source"`
	Skill      string  `json:"skill"`
	MinVersion *string `json:"min_version,omitempty"`
	MaxVersion *string `json:"max_version,omitempty"`
}

// ScanProblem is a definition the indexer rejected.
type ScanProblem struct {
	Agent   *string `json:"agent,omitempty"`
	Path    *string `json:"path,omitempty"`
	Message string  `json:"message"`
}

// HarnessTarget is how the CLI would install the Agent for one Harness.
type HarnessTarget struct {
	Harness    string   `json:"harness"`
	Status     string   `json:"status"                jsonschema:"native, generated (installable); incompatible or invalid (not installable, see reason)"`
	Reason     *string  `json:"reason,omitempty"`
	SourcePath *string  `json:"source_path,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

// Agent is one Agent of a repository's indexed snapshot.
type Agent struct {
	ID           string          `json:"id"`
	Description  *string         `json:"description,omitempty"`
	FromManifest bool            `json:"from_manifest"`
	Portable     *SourceFile     `json:"portable,omitempty"`
	Variants     []Variant       `json:"variants,omitempty"`
	Dependencies Dependencies    `json:"dependencies"`
	Problems     []ScanProblem   `json:"problems,omitempty"`
	Targets      []HarnessTarget `json:"targets,omitempty"`
	Adoption     Adoption        `json:"adoption"`
	AgnthubStars int             `json:"agnthub_stars"`
}

// AgentDetail is one Agent with its repository.
type AgentDetail struct {
	Repository RepositoryInfo `json:"repository"`
	Agent      Agent          `json:"agent"`
}
