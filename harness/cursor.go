package harness

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/agnthub-sh/agnthub/agent"
)

// cursorAdapter installs Cursor subagents (Markdown with YAML frontmatter).
//
// Cursor does not parse the frontmatter as YAML: it reads one "key: value" pair
// per line, lowercases keys and keeps values verbatim, and only `readonly: true`
// enables its read-only permission mode. Validation and conversion therefore
// check what Cursor itself will read, not only what YAML decodes.
type cursorAdapter struct{}

// cursorFields are the documented Cursor subagent frontmatter controls.
var cursorFields = map[string]bool{
	"name":          true,
	"description":   true,
	"model":         true,
	"readonly":      true,
	"is_background": true,
}

var cursorNameSeparators = regexp.MustCompile(`[\s_]+`)

func (cursorAdapter) ID() agent.HarnessID { return agent.Cursor }

func (cursorAdapter) Name() string { return "Cursor" }

func (cursorAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".cursor/agents", Suffixes: []string{".md"}}
}

func (cursorAdapter) Settings() []SettingSpec { return nil }

func (cursorAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	name, _ := doc.Frontmatter["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = cursorFileName(filePath)
	}
	id, display := namedIdentity(filePath, name)
	if id == "" {
		return NativeAgent{}, fmt.Errorf(
			"%s: the name %q is a display name and the file name implies no identifier",
			filePath, name,
		)
	}
	identity := NativeAgent{ID: id, DisplayName: display}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return identity, fmt.Errorf("%s: frontmatter requires a description", filePath)
	}
	identity.Description = description
	return identity, nil
}

// cursorFileName is the name Cursor derives from a file without a declared name.
func cursorFileName(filePath string) string {
	base := path.Base(filepath.ToSlash(filePath))
	return cursorNameSeparators.ReplaceAllString(strings.TrimSuffix(base, path.Ext(base)), "-")
}

func (cursorAdapter) Destination(ctx Context, scope Scope, _ Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return Destination{
			Dir:    filepath.Join(ctx.ProjectRoot, ".cursor", "agents"),
			Label:  "project",
			Shared: true,
		}, nil
	case ScopeGlobal:
		return Destination{Dir: cursorAgentsDir(ctx), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

func cursorAgentsDir(ctx Context) string {
	return filepath.Join(ctx.Env.Home(), ".cursor", "agents")
}

// Native installs an authored subagent as written, except a description
// Cursor would not read whole (a block scalar or a value continued on more
// lines, of which Cursor keeps the first line only): that line is rewritten as
// one line with the same words, and every other byte is kept.
func (cursorAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	content := variant.Raw
	notes, reason := validateCursorVariant(id, variant.Path, content)
	if reason == cursorDescriptionReason {
		if folded, ok := cursorFoldDescription(variant.Path, content); ok {
			content = folded
			notes, reason = validateCursorVariant(id, variant.Path, folded)
		}
	}
	if reason != "" {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Cursor,
			Path:    variant.Path,
			Reason:  reason,
		}
	}
	return Artifact{FileName: id + ".md", Content: content, Notes: notes}, nil
}

// validateCursorVariant returns why Cursor would not load raw as id with its
// declared behavior, or "" when it would, plus notes on documentation-only keys
// Cursor ignores.
func validateCursorVariant(id, filePath string, raw []byte) ([]string, string) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return nil, err.Error()
	}
	var notes []string
	for _, key := range sortedKeys(doc.Frontmatter) {
		switch {
		case cursorFields[key] || harmlessPortableKeys[key]:
		case descriptiveKeys[key]:
			notes = append(notes, key+" is not used by Cursor")
		case key == "tools":
			if _, ok := nameList(doc.Frontmatter[key]); !ok {
				return nil, "tools has a shape agnthub cannot read"
			}
			notes = append(notes, "Cursor does not enforce tools: on Cursor the agent gets "+
				"every tool unless readonly; other harnesses enforce it")
		default:
			return nil, fmt.Sprintf(
				"Cursor does not document %q; its behavior would not be enforced", key,
			)
		}
	}
	if strings.TrimSpace(doc.String("description")) == "" {
		return nil, "frontmatter requires a description"
	}
	if strings.TrimSpace(doc.Body) == "" {
		return nil, "Cursor skips subagents without instructions"
	}
	if value, ok := doc.Frontmatter["model"]; ok {
		if model, isString := value.(string); !isString || strings.TrimSpace(model) == "" {
			return nil, "model must be a model ID or inherit"
		}
	}
	for _, key := range []string{"readonly", "is_background"} {
		if value, ok := doc.Frontmatter[key]; ok {
			if _, isBool := value.(bool); !isBool {
				return nil, key + " must be a boolean"
			}
		}
	}
	if reason := cursorReadsAs(id, doc); reason != "" {
		return nil, reason
	}
	return notes, ""
}

// cursorDescriptionReason is why Cursor would not read a description whole.
const cursorDescriptionReason = "Cursor reads one line per key; write the description on a " +
	"single line"

// cursorFoldDescription rewrites the description of an authored subagent onto
// one line, its words joined by single spaces.
func cursorFoldDescription(filePath string, raw []byte) ([]byte, bool) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return nil, false
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok {
		return nil, false
	}
	_, line, ok := cursorDescription(strings.Join(strings.Fields(description), " "))
	if !ok {
		return nil, false
	}
	folded, err := agent.ReplaceKey(filePath, raw, "description", line)
	return folded, err == nil
}

// cursorReadsAs compares what Cursor's line reader sees with the decoded YAML and
// returns why they disagree on identity, restrictions or description. The
// description comes last: Native folds a description only when Cursor reads
// everything else as declared.
func cursorReadsAs(id string, doc *agent.Document) string {
	seen := cursorFrontmatter(doc.Raw)
	name, declared := doc.Frontmatter["name"]
	label, _ := name.(string)
	display := declared && agent.DisplayName(label)
	switch {
	case declared && !display && name != id:
		return fmt.Sprintf("name %v does not match agent %q", name, id)
	case declared && seen["name"] != strings.TrimSpace(label):
		return fmt.Sprintf(
			"Cursor reads the name as %q; write name: %s on one line",
			seen["name"],
			strings.TrimSpace(label),
		)
	case !declared && cursorFileName(id) != id:
		return fmt.Sprintf(
			"Cursor would name the file %q; declare name: %s",
			cursorFileName(id),
			id,
		)
	}
	for _, key := range []string{"readonly", "is_background"} {
		declaredTrue, _ := doc.Frontmatter[key].(bool)
		readTrue := strings.EqualFold(seen[key], "true")
		if key == "is_background" && seen["background"] != "" {
			readTrue = strings.EqualFold(seen["background"], "true")
		}
		if declaredTrue != readTrue {
			return fmt.Sprintf(
				"Cursor would read %s as %t but the frontmatter declares %t; write it once as a top-level %q line",
				key,
				readTrue,
				declaredTrue,
				fmt.Sprintf("%s: %t", key, declaredTrue),
			)
		}
	}
	if !cursorReadsDescription(seen["description"], doc.String("description")) {
		return cursorDescriptionReason
	}
	return ""
}

// cursorFrontmatter mirrors Cursor's subagent frontmatter reader: every non-empty,
// non-comment line with a colon sets a lowercased key to the trimmed raw value.
func cursorFrontmatter(raw []byte) map[string]string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	fields := map[string]string{}
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return fields
	}
	header, _, _ := strings.Cut(rest, "\n---")
	for line := range strings.SplitSeq(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		key = strings.ToLower(strings.TrimSpace(key))
		if !found || key == "" {
			continue
		}
		fields[key] = strings.TrimSpace(value)
	}
	return fields
}

// unquote strips one pair of matching YAML quotes Cursor would keep verbatim.
func unquote(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == last && (first == '"' || first == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// cursorReadsDescription reports whether raw, the line value Cursor reads,
// carries description: the same text, inside quotes Cursor keeps, or a quoted
// YAML scalar whose escapes (\" or ”) Cursor shows as written.
func cursorReadsDescription(raw, description string) bool {
	if unquote(raw) == description {
		return true
	}
	if raw == "" || raw[0] != '"' && raw[0] != '\'' {
		return false
	}
	var decoded string
	return yaml.Unmarshal([]byte(raw), &decoded) == nil && decoded == description
}

// cursorDescription renders a one-line description as a YAML scalar Cursor
// reads, returning the scalar and its frontmatter line. Cursor keeps a value as
// written, so a form it reads unchanged (plain, or quoted without escapes)
// comes first; a description that needs quoting and contains quote characters
// takes the quoted form with the fewest escapes, which Cursor shows as written.
func cursorDescription(description string) (*yaml.Node, string, bool) {
	var best *yaml.Node
	var bestLine string
	for _, style := range []yaml.Style{0, yaml.SingleQuotedStyle, yaml.DoubleQuotedStyle} {
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: style, Value: description}
		rendered, err := agent.RenderMarkdown([]agent.Field{{Key: "description", Value: node}}, "")
		if err != nil {
			continue
		}
		line := strings.TrimSuffix(strings.TrimPrefix(string(rendered), "---\n"), "\n---\n")
		raw, found := strings.CutPrefix(line, "description: ")
		if !found || strings.Contains(raw, "\n") || !cursorReadsDescription(raw, description) {
			continue
		}
		if unquote(raw) == description {
			return node, line, true
		}
		if best == nil || len(line) < len(bestLine) {
			best, bestLine = node, line
		}
	}
	return best, bestLine, best != nil
}

// readOnlyCaps are what a read-only agent keeps: Cursor's readonly blocks
// edits, every shell command and MCP tool calls, and Claude Code's plan mode
// converts the same way. Delegation is not carried, since a delegated agent
// could edit.
const readOnlyCaps = CapRead | CapSearch | CapWeb | CapTodo | CapSkill

func (a cursorAdapter) Semantics(id string, variant *agent.VariantFile) (*Semantics, error) {
	if _, err := a.Native(id, variant); err != nil {
		return nil, err
	}
	doc, _ := agent.ParseMarkdown(variant.Path, variant.Raw)
	sem := &Semantics{
		ID:           id,
		Description:  doc.String("description"),
		Instructions: doc.Body,
		DisplayName:  displayName(id, doc.String("name")),
		Source:       agent.Cursor,
	}
	for _, key := range sortedKeys(doc.Frontmatter) {
		value := doc.Frontmatter[key]
		switch key {
		case "name", "description":
		case "model":
			sem.Model, _ = value.(string)
		case "readonly":
			if value == true {
				sem.intersectTools(key, ToolPolicy{Restricted: true, Allowed: readOnlyCaps})
				sem.note("Cursor readonly: converted without edits, shell, MCP tools or delegation")
			}
		case "is_background":
			sem.Background = value == true
		case "tools":
			// Cursor ignores tools; authors copy it from Claude Code agents, so
			// other harnesses read it as a Claude Code allowlist.
			sem.relaxed = true
			listed := &Semantics{Source: agent.Cursor}
			claudeToolPolicy(listed, true, claudeToolEntries(listed, key, value), nil)
			sem.Notes = append(sem.Notes, listed.Notes...)
			sem.Unsupported = append(sem.Unsupported, listed.Unsupported...)
			sem.intersectTools(key, listed.Tools)
			sem.note("read tools as the tool allowlist: Cursor ignores it, other harnesses " +
				"enforce it")
		default:
			sem.note("omitted %s: descriptive metadata", key)
		}
	}
	return sem, nil
}

// Convert writes a Cursor subagent. Cursor has no tool allowlist: readonly: true
// is its only restriction and blocks edits, every shell command and MCP tool
// calls together. An Agent without edits becomes read-only and goes without
// its shell and MCP tools, as reductions; one that withholds shell but edits
// is refused, since Cursor cannot withhold shell alone.
func (a cursorAdapter) Convert(sem *Semantics) (Artifact, error) {
	check, err := begin(agent.Cursor, sem)
	if err != nil {
		return Artifact{}, err
	}
	sem = check.sem
	if err := check.requireWithheld(CapRead|CapSearch, "Cursor cannot withhold it"); err != nil {
		return Artifact{}, err
	}
	readonly := !sem.Tools.Allows(CapEdit)
	if !readonly && !sem.Tools.Allows(CapShell) {
		return Artifact{}, check.refuse("it excludes shell but allows edits, and Cursor can " +
			"only withhold shell by making the agent read-only")
	}
	if readonly && sem.Tools.Allows(CapShell) {
		check.reduce(ReducedShell, "Cursor's only restriction, readonly, blocks every shell "+
			"command")
	}
	// An agent without a tool list uses the session's MCP tools too.
	if readonly && (len(sem.Tools.MCP) > 0 || !sem.Tools.Restricted) {
		check.reduce("MCP tools", "Cursor's only restriction, readonly, blocks every MCP "+
			"tool call")
	}
	check.noteWithheld(CapWeb|CapDelegate, "Cursor cannot withhold them from an agent")
	if sem.Tools.Restricted && !readonly {
		check.note("Cursor agents keep the session's MCP tools unless read-only")
		check.mcpSession()
	}
	instructions := check.carry(additiveSupport{
		userMemory:    cursorUserMemory,
		loadSkill:     "Each one is listed among your available skills: read its SKILL.md.",
		sessionSkills: true,
		tools:         sem.Tools,
	})
	// Cursor reads one line per key, so the description's words are joined
	// onto one line, which keeps its meaning.
	description, _, ok := cursorDescription(strings.Join(strings.Fields(sem.Description), " "))
	if !ok {
		return Artifact{}, check.refuse("description cannot be written on one line Cursor " +
			"reads; author a native cursor variant")
	}
	fields := []agent.Field{
		{Key: "name", Value: sem.ID},
		{Key: "description", Value: description},
	}
	if readonly {
		fields = append(fields, agent.Field{Key: "readonly", Value: true})
	}
	if sem.Background {
		fields = append(fields, agent.Field{Key: "is_background", Value: true})
	}
	content, err := agent.RenderMarkdown(fields, instructions)
	if err != nil {
		return Artifact{}, err
	}
	return selfCheck(a, sem.ID, check.artifact(sem.ID+".md", content))
}

// cursorUserMemory holds user-scope memories on Cursor: its agents may write
// Markdown under ~/.cursor without approval, but nowhere else outside the
// workspace.
const cursorUserMemory = "~/.cursor/agent-memory/"

func (cursorAdapter) Detect(ctx Context) bool {
	return detect(ctx, "cursor-agent", filepath.Join(ctx.Env.Home(), ".cursor")) ||
		detect(ctx, "cursor")
}
