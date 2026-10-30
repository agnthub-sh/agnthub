// Package source parses source locators and exposes source snapshots as inert,
// read-only trees.
package source

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agnthub-sh/agnthub/platform/env"
)

// Kind is the type of a source.
type Kind string

// Source kinds.
const (
	KindLocal  Kind = "local"
	KindGitHub Kind = "github"
	KindGit    Kind = "git"
)

// GitHubHost is the only host indexed by the public registry.
const GitHubHost = "github.com"

// Locator identifies a source and an optional Git ref.
type Locator struct {
	Kind Kind
	// Path is the absolute directory of a local source.
	Path string
	// Host is the Git host (github.com for GitHub sources).
	Host string
	// Owner and Repo identify a GitHub repository.
	Owner string
	Repo  string
	// RepoPath is the path of a generic Git repository on Host.
	RepoPath string
	// CloneURL is the URL handed to git, without credentials.
	CloneURL string
	// Ref is the requested branch, tag or commit; empty selects the default revision.
	Ref string
}

var (
	// ErrEmptyLocator reports an empty source argument.
	ErrEmptyLocator = errors.New("source is required")

	githubShorthand = regexp.MustCompile(
		`^([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))/([A-Za-z0-9._-]{1,100})$`,
	)
	refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+@-]{0,254}$`)
	scpLike    = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):([^:].*)$`)
)

// Parse interprets a user-supplied source argument. Local paths must be explicit
// (start with ".", "/" or "~"); everything else is a Git locator.
func Parse(input, cwd string, environment env.Env) (Locator, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Locator{}, ErrEmptyLocator
	}
	if isLocal(input) {
		return parseLocal(input, cwd, environment)
	}
	spec, ref, err := splitRef(input)
	if err != nil {
		return Locator{}, err
	}
	var loc Locator
	switch {
	case githubShorthand.MatchString(spec):
		loc, err = gitHubLocator(spec)
	case strings.HasPrefix(spec, GitHubHost+"/"):
		loc, err = gitHubLocator(strings.TrimPrefix(spec, GitHubHost+"/"))
	case scpLike.MatchString(spec):
		loc, err = parseSCP(spec)
	default:
		loc, err = parseURL(spec)
	}
	if err != nil {
		return Locator{}, err
	}
	loc.Ref = ref
	return loc, nil
}

// ErrLocalReference reports repository metadata naming a local directory.
var ErrLocalReference = errors.New(
	"repository metadata may only reference Git sources, not local paths",
)

// ParseRemote interprets a source locator named by repository metadata, such as an
// external Agent Dependency. Metadata never selects a directory on the user's machine.
func ParseRemote(input string) (Locator, error) {
	if isLocal(strings.TrimSpace(input)) {
		return Locator{}, fmt.Errorf("%q: %w", input, ErrLocalReference)
	}
	return Parse(input, "", env.Env{})
}

func isLocal(input string) bool {
	if input == "." || input == ".." || input == "~" {
		return true
	}
	for _, prefix := range []string{"./", "../", "/", "~/", `.\`, `..\`} {
		if strings.HasPrefix(input, prefix) {
			return true
		}
	}
	return len(input) > 2 && input[1] == ':' && (input[2] == '\\' || input[2] == '/')
}

func parseLocal(input, cwd string, environment env.Env) (Locator, error) {
	path := input
	if path == "~" || strings.HasPrefix(path, "~/") {
		home := environment.Home()
		if home == "" {
			return Locator{}, errors.New("cannot expand ~: home directory is unknown")
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return Locator{Kind: KindLocal, Path: filepath.Clean(path)}, nil
}

func splitRef(input string) (spec, ref string, err error) {
	spec, ref, found := strings.Cut(input, "#")
	if !found {
		return input, "", nil
	}
	if !refPattern.MatchString(ref) || strings.Contains(ref, "..") {
		return "", "", fmt.Errorf("invalid git ref %q", ref)
	}
	return spec, ref, nil
}

func gitHubLocator(ownerRepo string) (Locator, error) {
	ownerRepo = strings.TrimSuffix(strings.TrimSuffix(ownerRepo, "/"), ".git")
	match := githubShorthand.FindStringSubmatch(ownerRepo)
	if match == nil || match[2] == "." || match[2] == ".." {
		return Locator{}, fmt.Errorf("invalid GitHub repository %q (want owner/repo)", ownerRepo)
	}
	return Locator{
		Kind:     KindGitHub,
		Host:     GitHubHost,
		Owner:    match[1],
		Repo:     match[2],
		CloneURL: "https://" + GitHubHost + "/" + match[1] + "/" + match[2] + ".git",
	}, nil
}

func parseSCP(spec string) (Locator, error) {
	match := scpLike.FindStringSubmatch(spec)
	host, repoPath := strings.ToLower(match[2]), strings.TrimPrefix(match[3], "/")
	if err := validateRepoPath(repoPath); err != nil {
		return Locator{}, err
	}
	if host == GitHubHost {
		loc, err := gitHubLocator(repoPath)
		if err != nil {
			return Locator{}, err
		}
		loc.CloneURL = match[1] + "@" + GitHubHost + ":" + loc.Owner + "/" + loc.Repo + ".git"
		return loc, nil
	}
	return Locator{Kind: KindGit, Host: host, RepoPath: repoPath, CloneURL: spec}, nil
}

func parseURL(spec string) (Locator, error) {
	parsed, err := url.Parse(spec)
	if err != nil || parsed.Host == "" {
		return Locator{}, fmt.Errorf(
			"unrecognized source %q: use owner/repo, a Git URL or an explicit local path such as ./dir",
			spec,
		)
	}
	switch parsed.Scheme {
	case "https", "ssh":
	default:
		return Locator{}, fmt.Errorf(
			"unsupported source scheme %q: use https or ssh",
			parsed.Scheme,
		)
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return Locator{}, errors.New(
			"source URL contains a password; configure a Git credential helper instead",
		)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return Locator{}, errors.New("source URL must not contain a query string")
	}
	host := strings.ToLower(parsed.Hostname())
	repoPath := strings.Trim(parsed.Path, "/")
	if err := validateRepoPath(repoPath); err != nil {
		return Locator{}, err
	}
	if host == GitHubHost {
		loc, err := gitHubLocator(repoPath)
		if err != nil {
			return Locator{}, err
		}
		if parsed.Scheme == "ssh" {
			loc.CloneURL = spec
		}
		return loc, nil
	}
	// A non-default port is part of the repository's identity.
	return Locator{
		Kind:     KindGit,
		Host:     strings.ToLower(parsed.Host),
		RepoPath: repoPath,
		CloneURL: spec,
	}, nil
}

func validateRepoPath(repoPath string) error {
	if repoPath == "" {
		return errors.New("source URL has no repository path")
	}
	for segment := range strings.SplitSeq(repoPath, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, "-") {
			return fmt.Errorf("invalid repository path %q", repoPath)
		}
	}
	return nil
}

// Key is the stable, credential-free identity of the source (without ref).
func (l Locator) Key() string {
	switch l.Kind {
	case KindGitHub:
		return GitHubHost + "/" + strings.ToLower(l.Owner) + "/" + strings.ToLower(l.Repo)
	case KindGit:
		return l.Host + "/" + strings.TrimSuffix(l.RepoPath, ".git")
	default:
		return "local:" + filepath.ToSlash(l.Path)
	}
}

// Display is a short human-readable form including the ref.
func (l Locator) Display() string {
	var base string
	switch l.Kind {
	case KindGitHub:
		base = l.Owner + "/" + l.Repo
	case KindGit:
		base = l.Host + "/" + l.RepoPath
	default:
		base = l.Path
	}
	if l.Ref != "" {
		return base + "#" + l.Ref
	}
	return base
}

// IsRemote reports whether the source is fetched with Git.
func (l Locator) IsRemote() bool {
	return l.Kind != KindLocal
}

// TransportSSH names a Git source fetched over SSH in locks.
const TransportSSH = "ssh"

// SSHURL returns the clone URL of a source fetched over SSH (scp-like
// user@host:path or ssh://), or "" for https and local sources. Parse refuses
// passwords, so the URL is credential-free and safe to record in a lock.
func (l Locator) SSHURL() string {
	if l.Kind == KindLocal {
		return ""
	}
	if strings.HasPrefix(l.CloneURL, "ssh://") || scpLike.MatchString(l.CloneURL) {
		return l.CloneURL
	}
	return ""
}
