package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"charm.land/huh/v2"
)

// Option is one choice in a multi-select prompt.
type Option struct {
	Value    string
	Label    string
	Selected bool
}

// Prompter asks an interactive user for selections and approvals. It is only used
// when the App is interactive and no flag already decided the answer.
type Prompter interface {
	// MultiSelect returns the chosen option values; at least one is required.
	MultiSelect(ctx context.Context, title, description string, options []Option) ([]string, error)
	// Confirm asks a yes/no question whose default is no.
	Confirm(ctx context.Context, title, description string) (bool, error)
}

// errCancelled reports a prompt the user aborted.
var errCancelled = errors.New("cancelled; nothing was written")

// formPrompter renders prompts with huh: a terminal UI, or numbered line prompts
// in accessible mode for screen readers and dumb terminals.
type formPrompter struct {
	in         io.Reader
	out        io.Writer
	accessible bool
}

func (p *formPrompter) MultiSelect(
	ctx context.Context,
	title, description string,
	options []Option,
) ([]string, error) {
	choices := make([]huh.Option[string], 0, len(options))
	var value []string
	for _, option := range options {
		choices = append(choices, huh.NewOption(option.Label, option.Value))
		if option.Selected {
			value = append(value, option.Value)
		}
	}
	// An empty submission asks again rather than showing an error before any key.
	for {
		lines := 1 + len(choices)
		if description != "" {
			lines += 1 + strings.Count(description, "\n")
		}
		field := huh.NewMultiSelect[string]().
			Title(title).
			Description(description).
			Value(&value).
			Options(choices...).
			Filterable(false).
			Height(lines)
		if err := p.run(ctx, field); err != nil {
			return nil, err
		}
		if len(value) > 0 {
			return value, nil
		}
		if !strings.HasPrefix(description, selectOne) {
			description = strings.TrimSpace(selectOne + "\n" + description)
		}
	}
}

const selectOne = "Select at least one (space toggles, enter confirms)."

func (p *formPrompter) Confirm(ctx context.Context, title, description string) (bool, error) {
	var confirmed bool
	field := huh.NewConfirm().
		Title(title).
		Description(description).
		Affirmative("Apply").
		Negative("Cancel").
		Value(&confirmed)
	if err := p.run(ctx, field); err != nil {
		return false, err
	}
	return confirmed, nil
}

func (p *formPrompter) run(ctx context.Context, field huh.Field) error {
	err := huh.NewForm(huh.NewGroup(field)).
		WithInput(p.in).
		WithOutput(p.out).
		WithAccessible(p.accessible).
		WithTheme(huh.ThemeFunc(huh.ThemeBase16)).
		WithShowHelp(true).
		RunWithContext(ctx)
	if errors.Is(err, huh.ErrUserAborted) {
		return errCancelled
	}
	return err
}
