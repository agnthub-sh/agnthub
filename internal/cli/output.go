package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/discovery"
	"github.com/agnthub-sh/agnthub/source"
)

var jsonOut = sonic.Config{SortMapKeys: true, EscapeHTML: false}.Froze()

type printer struct {
	w      io.Writer
	json   bool
	styles planStyles
	// plain output carries no escape sequences and no emoji.
	plain bool
	// width is the terminal width tables fit; 0 for unlimited.
	width int
	// root and home shorten displayed paths.
	root, home string
	// space separates blocks of human output; nil for JSON.
	space *spacer
}

func newPrinter(w io.Writer, asJSON bool) *printer {
	return &printer{w: w, json: asJSON, styles: newPlanStyles()}
}

func (p *printer) emitJSON(value any) error {
	content, err := jsonOut.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(p.w, "%s\n", content)
	return err
}

type inventoryJSON struct {
	Source     string             `json:"source"`
	Revision   string             `json:"revision,omitempty"`
	Manifest   bool               `json:"manifest"`
	Categories []string           `json:"categories,omitempty"`
	Agents     []inventoryAgent   `json:"agents"`
	Problems   []inventoryProblem `json:"problems"`
	// Ignore is what the source asks agnthub to ignore, when it asks anything.
	Ignore *inventoryIgnore `json:"ignore,omitempty"`
}

type inventoryIgnore struct {
	// All ignores the whole source; Paths ignore files and directories in it.
	All   bool     `json:"all"`
	Paths []string `json:"paths"`
	// Applied is false with --include-ignored, which lists ignored agents too.
	Applied bool `json:"applied"`
}

type inventoryAgent struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Categories  []string          `json:"categories,omitempty"`
	Portable    string            `json:"portable,omitempty"`
	Variants    map[string]string `json:"variants"`
	// Plugin is the Claude Code plugin that provides the agent's Claude Code
	// Variant, when the repository packages it as a plugin.
	Plugin string `json:"plugin,omitempty"`
	// Division is the catalog-layout folder the agent comes from.
	Division string `json:"division,omitempty"`
}

type inventoryProblem struct {
	Agent   string `json:"agent,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (p *printer) inventory(
	snapshot *source.Snapshot,
	inv *discovery.Inventory,
	includeIgnored bool,
) error {
	payload := inventoryJSON{
		Source:     snapshot.Locator.Display(),
		Revision:   snapshot.Revision,
		Manifest:   inv.Manifest,
		Categories: inv.Categories,
		Agents:     []inventoryAgent{},
		Problems:   []inventoryProblem{},
	}
	if inv.Ignore.Set() {
		payload.Ignore = &inventoryIgnore{
			All: inv.Ignore.All, Paths: append([]string{}, inv.Ignore.Paths...),
			Applied: !includeIgnored,
		}
	}
	for _, def := range inv.Agents {
		item := inventoryAgent{
			ID: def.ID, Description: def.Description, Categories: inv.CategoriesFor(def),
			Variants: map[string]string{}, Division: def.Division,
		}
		if def.Portable != nil {
			item.Portable = def.Portable.Path
		}
		for harnessID, variant := range def.Variants {
			item.Variants[string(harnessID)] = variant.Path
		}
		if variant := def.Variant(agent.Claude); variant != nil {
			item.Plugin = variant.Plugin
		}
		payload.Agents = append(payload.Agents, item)
	}
	for _, problem := range inv.Problems {
		payload.Problems = append(payload.Problems, inventoryProblem(problem))
	}
	if p.json {
		return p.emitJSON(payload)
	}
	s := p.styles
	header := payload.Source
	if payload.Revision != "" {
		header += "@" + shortRevision(payload.Revision)
	}
	if inv.Manifest {
		header += " (agnthub.yaml)"
	}
	fmt.Fprintln(p.w, s.title.Render(header))
	ignore := payload.Ignore
	switch {
	case ignore == nil:
	case !ignore.Applied:
		fmt.Fprintln(p.w, s.dim.Render("  ignore rules not applied (--"+includeIgnoredFlag+"): "+
			ignoreRules(ignore)))
	case ignore.All:
		fmt.Fprintf(p.w, "  ignored: the source asks agnthub to ignore it (%s, or ignore: all in "+
			"%s); --%s lists its agents anyway\n", discovery.IgnoreFile, discovery.ManifestFile,
			includeIgnoredFlag)
	default:
		fmt.Fprintln(p.w, s.dim.Render(fmt.Sprintf("  ignored paths: %s (--%s lists their agents)",
			strings.Join(ignore.Paths, ", "), includeIgnoredFlag)))
	}
	if len(payload.Agents) == 0 && (ignore == nil || !ignore.All || !ignore.Applied) {
		fmt.Fprintln(p.w, "  no installable agents found")
	}
	for _, item := range payload.Agents {
		fmt.Fprintf(p.w, "  %s  %s\n", s.heading.Render(item.ID), oneLine(item.Description))
		var forms []string
		if item.Portable != "" {
			forms = append(forms, "portable "+item.Portable)
		}
		for _, harnessID := range agent.AllHarnesses {
			if path, ok := item.Variants[string(harnessID)]; ok {
				forms = append(forms, string(harnessID)+" "+path)
			}
		}
		if item.Plugin != "" {
			forms = append(forms, "plugin "+item.Plugin)
		}
		if item.Division != "" {
			forms = append(forms, "division "+item.Division)
		}
		fmt.Fprintf(p.w, "      %s\n", s.dim.Render(strings.Join(forms, "; ")))
	}
	if len(payload.Problems) > 0 {
		fmt.Fprintln(p.w, s.blocked.Render("problems:"))
		for _, problem := range inv.Problems {
			fmt.Fprintf(p.w, "  ! %s\n", problem)
		}
	}
	if len(payload.Agents) > 0 {
		fmt.Fprintln(p.w, s.dim.Render(
			"Install with --agent <id> (repeatable, '*' for all) and --harness <id> (all for "+
				"every harness), or run add without them to choose interactively."))
	}
	return nil
}

// ignoreRules describes the rules a source declares.
func ignoreRules(ignore *inventoryIgnore) string {
	if ignore.All {
		return "the whole source"
	}
	return strings.Join(ignore.Paths, ", ")
}

func formatSettings(settings map[string]string) string {
	keys := slices.Sorted(maps.Keys(settings))
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+settings[key])
	}
	return strings.Join(parts, ", ")
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 100 {
		return text
	}
	cut := 97
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut-- // never split a multi-byte character ("—", "é", emoji)
	}
	return text[:cut] + "..."
}
