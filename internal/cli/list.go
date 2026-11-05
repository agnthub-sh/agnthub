package cli

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/state"
)

type listOptions struct {
	global    bool
	json      bool
	harnesses []string
}

func (a *App) listCommand() *cobra.Command {
	opts := &listOptions{}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List agents agnthub installed in this project or globally (alias: ls)",
		Example: `  agnthub list
  agnthub ls -g
  agnthub ls -H claude
  agnthub ls --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runList(opts)
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&opts.global, "global", "g", false, "list user-scope installations")
	flags.StringArrayVarP(&opts.harnesses, "harness", "H", nil,
		"only list installations for this harness (repeatable)")
	flags.BoolVar(&opts.json, "json", false, "print machine-readable JSON")
	return cmd
}

type listedArtifact struct {
	Harness  string              `json:"harness"`
	Variant  string              `json:"variant"`
	Path     string              `json:"path"`
	Status   string              `json:"status"`
	Bindings []map[string]string `json:"bindings"`
}

type listedAgent struct {
	Agent     string           `json:"agent"`
	Source    sourceJSON       `json:"source"`
	Origin    string           `json:"origin"`
	Artifacts []listedArtifact `json:"artifacts"`
}

func (a *App) runList(opts *listOptions) error {
	filter := make([]string, 0, len(opts.harnesses))
	for _, name := range opts.harnesses {
		id, err := agent.ParseHarness(name)
		if err != nil {
			return err
		}
		filter = append(filter, string(id))
	}
	global, err := a.lockScope(opts.global, false)
	if err != nil {
		return err
	}
	lockPath := state.LockPath(a.WorkDir, a.Env.ConfigHome(), global)
	lock, err := state.LoadLock(lockPath)
	if err != nil {
		return err
	}
	ownership, err := state.LoadOwnership(state.OwnershipPath(a.Env.StateHome()))
	if err != nil {
		return err
	}
	listed := make([]listedAgent, 0, len(lock.Agents))
	for _, locked := range lock.Agents {
		entry := listedAgent{
			Agent:  locked.Agent,
			Source: sourceJSON(locked.Source),
			Origin: string(locked.Origin),
		}
		for _, artifact := range locked.Artifacts {
			if len(filter) > 0 && !slices.Contains(filter, artifact.Harness) {
				continue
			}
			path := artifact.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(a.WorkDir, filepath.FromSlash(path))
			}
			item := listedArtifact{
				Harness:  artifact.Harness,
				Variant:  artifact.Variant,
				Path:     path,
				Status:   artifactStatus(path, artifact.Digest),
				Bindings: []map[string]string{},
			}
			for _, record := range ownership.ForPath(path) {
				if record.Harness == artifact.Harness {
					binding := map[string]string{}
					for key, value := range record.Settings {
						binding[key] = value
					}
					item.Bindings = append(item.Bindings, binding)
				}
			}
			entry.Artifacts = append(entry.Artifacts, item)
		}
		if len(entry.Artifacts) > 0 {
			listed = append(listed, entry)
		}
	}
	out := a.printer(opts.json)
	if opts.json {
		return out.emitJSON(listed)
	}
	if len(listed) == 0 {
		fmt.Fprintf(out.w, "No agents installed (%s).\n", lockPath)
		if !global {
			fmt.Fprintln(out.w, out.styles.dim.Render("Try listing user-scope agents with -g."))
		}
		return nil
	}
	a.listTable(out, listed)
	return nil
}

// listTable prints one row per installed artifact as borderless, aligned columns:
// styled in a terminal, plain text otherwise.
func (a *App) listTable(out *printer, listed []listedAgent) {
	s := out.styles
	t := &table{
		headers: []string{"AGENT", "HARNESS", "STATUS", "PATH", "SOURCE"},
		shrink:  3, wrap: -1,
	}
	for _, entry := range listed {
		name := entry.Agent
		if entry.Origin == string(state.OriginDependency) {
			name += " (dependency)"
		}
		for _, artifact := range entry.Artifacts {
			harnessCell := artifact.Harness
			for _, binding := range artifact.Bindings {
				if len(binding) > 0 {
					harnessCell += " [" + formatSettings(binding) + "]"
				}
			}
			status := cell{text: artifact.Status, style: s.blocked}
			if artifact.Status == localOK {
				status.style = s.create
			}
			t.rows = append(t.rows, []cell{
				{text: name, style: s.title},
				plainCell(harnessCell), status,
				pathCell(out.path(artifact.Path)),
				{text: sourceLabel(entry.Source), style: s.dim},
			})
		}
	}
	out.render(t)
}

func artifactStatus(path, digest string) string {
	onDisk, exists, err := state.FileDigest(path)
	switch {
	case err != nil:
		return "unreadable"
	case !exists:
		return "missing"
	case onDisk != digest:
		return "modified"
	default:
		return "ok"
	}
}
