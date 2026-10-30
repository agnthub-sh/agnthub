package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/heynemann/agnthub/state"
)

type listOptions struct {
	global bool
	json   bool
}

func (a *App) listCommand() *cobra.Command {
	opts := &listOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agents installed by agnthub in this project (or globally)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runList(opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.global, "global", "g", false, "list user-scope installations")
	cmd.Flags().BoolVar(&opts.json, "json", false, "print machine-readable JSON")
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
	lockPath := state.LockPath(a.WorkDir, a.Env.ConfigHome(), opts.global)
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
		listed = append(listed, entry)
	}
	out := newPrinter(a.Stdout, opts.json)
	if opts.json {
		return out.emitJSON(listed)
	}
	if len(listed) == 0 {
		fmt.Fprintf(a.Stdout, "No agents installed (%s).\n", lockPath)
		return nil
	}
	for _, entry := range listed {
		fmt.Fprintf(
			a.Stdout,
			"%s  from %s (%s)\n",
			entry.Agent,
			sourceLabel(entry.Source),
			entry.Origin,
		)
		for _, artifact := range entry.Artifacts {
			line := fmt.Sprintf(
				"  %-9s %-9s %-8s %s",
				artifact.Harness,
				artifact.Variant,
				artifact.Status,
				artifact.Path,
			)
			for _, binding := range artifact.Bindings {
				if len(binding) > 0 {
					line += " [" + formatSettings(binding) + "]"
				}
			}
			fmt.Fprintln(a.Stdout, line)
		}
	}
	return nil
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
