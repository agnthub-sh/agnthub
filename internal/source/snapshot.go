package source

import (
	"context"
	"errors"
	"fmt"
)

// Snapshot is one resolved, immutable view of a source.
type Snapshot struct {
	Locator Locator
	// Revision is the resolved commit for Git sources; empty for local directories.
	Revision string
	Tree     Tree
	closer   func() error
}

// Close releases resources held by the snapshot.
func (s *Snapshot) Close() error {
	if s.closer == nil {
		return nil
	}
	return s.closer()
}

// ErrRemoteUnsupported reports a Git source when no fetcher is configured.
var ErrRemoteUnsupported = errors.New("git sources are not supported by this opener")

// Fetcher resolves remote Git sources into snapshots.
type Fetcher interface {
	Fetch(ctx context.Context, loc Locator) (*Snapshot, error)
}

// Opener resolves locators into snapshots.
type Opener struct {
	// Git fetches remote sources; nil disables them.
	Git Fetcher
}

// Open resolves loc into a snapshot.
func (o Opener) Open(ctx context.Context, loc Locator) (*Snapshot, error) {
	if loc.Kind == KindLocal {
		tree, err := OpenLocal(loc.Path)
		if err != nil {
			return nil, err
		}
		return &Snapshot{Locator: loc, Tree: tree, closer: tree.Close}, nil
	}
	if o.Git == nil {
		return nil, fmt.Errorf("%s: %w", loc.Display(), ErrRemoteUnsupported)
	}
	return o.Git.Fetch(ctx, loc)
}

// NewSnapshot builds a snapshot from an already resolved tree.
func NewSnapshot(loc Locator, revision string, tree Tree, closer func() error) *Snapshot {
	return &Snapshot{Locator: loc, Revision: revision, Tree: tree, closer: closer}
}
