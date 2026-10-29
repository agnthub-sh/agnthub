package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/discovery"
	"github.com/heynemann/agnthub/internal/install"
	"github.com/heynemann/agnthub/internal/source"
	"github.com/heynemann/agnthub/internal/state"
)

var jsonOut = sonic.Config{SortMapKeys: true, EscapeHTML: false}.Froze()

type printer struct {
	w    io.Writer
	json bool
}

func newPrinter(w io.Writer, asJSON bool) *printer {
	return &printer{w: w, json: asJSON}
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
	Source   string             `json:"source"`
	Revision string             `json:"revision,omitempty"`
	Manifest bool               `json:"manifest"`
	Agents   []inventoryAgent   `json:"agents"`
	Problems []inventoryProblem `json:"problems"`
}

type inventoryAgent struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Portable    string            `json:"portable,omitempty"`
	Variants    map[string]string `json:"variants"`
}

type inventoryProblem struct {
	Agent   string `json:"agent,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (p *printer) inventory(snapshot *source.Snapshot, inv *discovery.Inventory) error {
	payload := inventoryJSON{
		Source:   snapshot.Locator.Display(),
		Revision: snapshot.Revision,
		Manifest: inv.Manifest,
		Agents:   []inventoryAgent{},
		Problems: []inventoryProblem{},
	}
	for _, def := range inv.Agents {
		item := inventoryAgent{
			ID: def.ID, Description: def.Description, Variants: map[string]string{},
		}
		if def.Portable != nil {
			item.Portable = def.Portable.Path
		}
		for harnessID, variant := range def.Variants {
			item.Variants[string(harnessID)] = variant.Path
		}
		payload.Agents = append(payload.Agents, item)
	}
	for _, problem := range inv.Problems {
		payload.Problems = append(payload.Problems, inventoryProblem(problem))
	}
	if p.json {
		return p.emitJSON(payload)
	}
	header := payload.Source
	if payload.Revision != "" {
		header += " @ " + shortRevision(payload.Revision)
	}
	if inv.Manifest {
		header += " (agnthub.yaml)"
	}
	fmt.Fprintln(p.w, header)
	if len(payload.Agents) == 0 {
		fmt.Fprintln(p.w, "  no installable agents found")
	}
	for _, item := range payload.Agents {
		fmt.Fprintf(p.w, "  %s  %s\n", item.ID, oneLine(item.Description))
		var forms []string
		if item.Portable != "" {
			forms = append(forms, "portable "+item.Portable)
		}
		for _, harnessID := range agent.AllHarnesses {
			if path, ok := item.Variants[string(harnessID)]; ok {
				forms = append(forms, string(harnessID)+" "+path)
			}
		}
		fmt.Fprintf(p.w, "      %s\n", strings.Join(forms, "; "))
	}
	if len(payload.Problems) > 0 {
		fmt.Fprintln(p.w, "problems:")
		for _, problem := range inv.Problems {
			fmt.Fprintf(p.w, "  ! %s\n", problem)
		}
	}
	return nil
}

type planJSON struct {
	Status string     `json:"status"`
	Scope  string     `json:"scope"`
	Source sourceJSON `json:"source"`
	Items  []itemJSON `json:"items"`
}

type sourceJSON struct {
	Kind     string `json:"kind"`
	Locator  string `json:"locator"`
	Ref      string `json:"ref,omitempty"`
	Revision string `json:"revision,omitempty"`
}

type itemJSON struct {
	Agent       string            `json:"agent"`
	Harness     string            `json:"harness"`
	Settings    map[string]string `json:"settings"`
	Destination string            `json:"destination,omitempty"`
	Path        string            `json:"path,omitempty"`
	Variant     string            `json:"variant,omitempty"`
	SourcePath  string            `json:"sourcePath,omitempty"`
	Source      sourceJSON        `json:"source"`
	Origin      string            `json:"origin"`
	RequiredBy  []string          `json:"requiredBy,omitempty"`
	Action      string            `json:"action"`
	Reason      string            `json:"reason,omitempty"`
	Notes       []string          `json:"notes,omitempty"`
}

func (p *printer) plan(plan *install.Plan, status string) {
	payload := planJSON{
		Status: status,
		Scope:  string(plan.Scope),
		Source: sourceJSON(plan.Source),
		Items:  make([]itemJSON, 0, len(plan.Items)),
	}
	for _, item := range plan.Items {
		entry := itemJSON{
			Agent:       item.Agent,
			Harness:     string(item.Harness),
			Settings:    map[string]string{},
			Destination: item.Destination.Label,
			Path:        item.Path,
			SourcePath:  item.SourcePath,
			Source:      sourceJSON(item.Source),
			Origin:      string(item.Origin),
			RequiredBy:  item.RequiredBy,
			Action:      string(item.Action),
			Reason:      item.Reason,
			Notes:       item.Artifact.Notes,
		}
		for key, value := range item.Settings {
			entry.Settings[key] = value
		}
		if item.Path != "" {
			entry.Variant = "native"
			if item.Artifact.Generated {
				entry.Variant = "generated"
			}
		}
		payload.Items = append(payload.Items, entry)
	}
	if p.json {
		_ = p.emitJSON(payload)
		return
	}
	fmt.Fprintf(
		p.w,
		"%s (%s scope) from %s\n",
		titleStatus(status),
		payload.Scope,
		sourceLabel(payload.Source),
	)
	for _, item := range payload.Items {
		marker := actionMarker(install.Action(item.Action))
		line := fmt.Sprintf("  %s %-10s %-9s %s", marker, item.Action, item.Harness, item.Agent)
		if item.Variant != "" {
			line += " (" + item.Variant + ")"
		}
		if len(item.Settings) > 0 {
			line += " [" + formatSettings(item.Settings) + "]"
		}
		fmt.Fprintln(p.w, line)
		switch {
		case item.Path != "" && item.Destination != "":
			fmt.Fprintf(p.w, "      → %s (%s)\n", item.Path, item.Destination)
		case item.Path != "":
			fmt.Fprintf(p.w, "      → %s\n", item.Path)
		}
		if item.Origin == string(state.OriginDependency) {
			fmt.Fprintf(
				p.w,
				"      dependency of %s, from %s\n",
				strings.Join(item.RequiredBy, ", "),
				sourceLabel(item.Source),
			)
		}
		if item.Reason != "" {
			fmt.Fprintf(p.w, "      %s\n", item.Reason)
		}
		for _, note := range item.Notes {
			fmt.Fprintf(p.w, "      note: %s\n", note)
		}
	}
}

func titleStatus(status string) string {
	switch status {
	case "refused":
		return "Refused before writing"
	case "applied":
		return "Installed"
	default:
		return "Plan"
	}
}

func sourceLabel(src sourceJSON) string {
	label := src.Locator
	if src.Ref != "" {
		label += "#" + src.Ref
	}
	if src.Revision != "" {
		label += " @ " + shortRevision(src.Revision)
	}
	return label
}

func actionMarker(action install.Action) string {
	switch {
	case action.Blocking():
		return "✗"
	case action == install.ActionUnchanged:
		return "="
	default:
		return "+"
	}
}

func formatSettings(settings map[string]string) string {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
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
	if len(text) > 100 {
		return text[:97] + "..."
	}
	return text
}
