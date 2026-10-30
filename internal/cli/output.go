package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/discovery"
	"github.com/heynemann/agnthub/source"
)

var jsonOut = sonic.Config{SortMapKeys: true, EscapeHTML: false}.Froze()

type printer struct {
	w      io.Writer
	json   bool
	styles planStyles
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
	if len(text) > 100 {
		return text[:97] + "..."
	}
	return text
}
