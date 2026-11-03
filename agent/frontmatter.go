package agent

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
	// ErrNoFrontmatter reports a Markdown file without a leading YAML block.
	ErrNoFrontmatter = errors.New("missing YAML frontmatter")
	utf8BOM          = []byte{0xEF, 0xBB, 0xBF}
)

// ParseMarkdown splits a Markdown document into YAML frontmatter and body.
func ParseMarkdown(path string, raw []byte) (*Document, error) {
	header, body, err := splitFrontmatter(path, raw)
	if err != nil {
		return nil, err
	}
	fields, err := decodeYAMLHeader(path, header)
	if err != nil {
		return nil, err
	}
	return &Document{Path: path, Raw: raw, Frontmatter: fields, Body: body}, nil
}

// ParseMarkdownLenient parses like ParseMarkdown but, when the header is not valid
// YAML, accepts a flat "key: value" header the way Claude Code does: one top-level
// key per line, the value being the rest of the line verbatim (so unquoted colons
// in descriptions survive). Every value from the fallback is a string.
func ParseMarkdownLenient(path string, raw []byte) (*Document, error) {
	header, body, err := splitFrontmatter(path, raw)
	if err != nil {
		return nil, err
	}
	fields, yamlErr := decodeYAMLHeader(path, header)
	if yamlErr != nil {
		var ok bool
		if fields, ok = flatHeader(header); !ok {
			return nil, yamlErr
		}
	}
	return &Document{Path: path, Raw: raw, Frontmatter: fields, Body: body}, nil
}

func splitFrontmatter(path string, raw []byte) (header, body string, err error) {
	text := string(bytes.TrimPrefix(raw, utf8BOM))
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", "", fmt.Errorf("%s: %w", path, ErrNoFrontmatter)
	}
	rest := text[len("---\n"):]
	switch {
	case strings.HasPrefix(rest, "---\n"):
		return "", rest[len("---\n"):], nil
	case rest == "---":
		return "", "", nil
	}
	end := strings.Index(rest, "\n---\n")
	if end >= 0 {
		return rest[:end], rest[end+len("\n---\n"):], nil
	}
	if !strings.HasSuffix(rest, "\n---") {
		return "", "", fmt.Errorf("%s: unterminated YAML frontmatter", path)
	}
	return rest[:len(rest)-len("\n---")], "", nil
}

func decodeYAMLHeader(path, header string) (map[string]any, error) {
	fields := map[string]any{}
	if strings.TrimSpace(header) == "" {
		return fields, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(header), &node); err != nil {
		return nil, fmt.Errorf("%s: invalid YAML frontmatter: %w", path, err)
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: frontmatter must be a YAML mapping", path)
	}
	if err := node.Content[0].Decode(&fields); err != nil {
		return nil, fmt.Errorf("%s: invalid YAML frontmatter: %w", path, err)
	}
	return fields, nil
}

// flatHeader parses one "key: value" per line; any other line shape fails.
func flatHeader(header string) (map[string]any, bool) {
	fields := map[string]any{}
	for line := range strings.SplitSeq(header, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found || !flatKey(key) {
			return nil, false
		}
		if _, dup := fields[key]; dup {
			return nil, false
		}
		fields[key] = strings.TrimSpace(value)
	}
	return fields, len(fields) > 0
}

func flatKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// Field is one ordered frontmatter entry.
type Field struct {
	Key   string
	Value any
}

// RenderMarkdown serializes ordered frontmatter fields followed by body.
func RenderMarkdown(fields []Field, body string) ([]byte, error) {
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	for _, field := range fields {
		var value yaml.Node
		if err := value.Encode(field.Value); err != nil {
			return nil, fmt.Errorf("encode frontmatter %s: %w", field.Key, err)
		}
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field.Key}
		mapping.Content = append(mapping.Content, key, &value)
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(mapping); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	buf.WriteString("---\n")
	buf.WriteString(body)
	return buf.Bytes(), nil
}

// MappingKeys returns the keys of the frontmatter mapping field in authored
// order, or nil when field is absent or not a mapping. Decoded frontmatter maps
// lose key order, which matters where a Harness applies the last matching rule.
func MappingKeys(raw []byte, field string) []string {
	header, _, err := splitFrontmatter("", raw)
	if err != nil {
		return nil
	}
	var node yaml.Node
	if yaml.Unmarshal([]byte(header), &node) != nil || len(node.Content) != 1 {
		return nil
	}
	root := node.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != field || root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		value := root.Content[i+1]
		keys := make([]string, 0, len(value.Content)/2)
		for j := 0; j+1 < len(value.Content); j += 2 {
			keys = append(keys, value.Content[j].Value)
		}
		return keys
	}
	return nil
}

// OmitKeys returns raw without the listed top-level frontmatter keys, removing
// only the lines that declare them so every other byte (BOM, line endings,
// comments, quoting, body) is kept. omitted lists the keys that were present,
// in the order given. It fails when the header cannot be edited line by line
// without changing anything else, such as a flow-style mapping.
func OmitKeys(path string, raw []byte, keys ...string) (out []byte, omitted []string, err error) {
	doc, err := ParseMarkdownLenient(path, raw)
	if err != nil {
		return nil, nil, err
	}
	for _, key := range keys {
		if _, ok := doc.Frontmatter[key]; ok {
			omitted = append(omitted, key)
		}
	}
	if len(omitted) == 0 {
		return raw, nil, nil
	}
	header, _, _ := splitFrontmatter(path, raw)
	drop := headerKeyLines(header, omitted)
	// Raw line 0 is the opening "---"; header line n (1-based) is raw line n.
	var buf bytes.Buffer
	buf.Grow(len(raw))
	line := 0
	for rest := raw; len(rest) > 0; line++ {
		end := bytes.IndexByte(rest, '\n') + 1
		if end == 0 {
			end = len(rest)
		}
		if !drop[line] {
			buf.Write(rest[:end])
		}
		rest = rest[end:]
	}
	out = buf.Bytes()
	edited, err := ParseMarkdownLenient(path, out)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%s: cannot omit %s: %w",
			path,
			strings.Join(omitted, ", "),
			err,
		)
	}
	want := maps.Clone(doc.Frontmatter)
	for _, key := range omitted {
		delete(want, key)
	}
	if edited.Body != doc.Body || !reflect.DeepEqual(edited.Frontmatter, want) {
		return nil, nil, fmt.Errorf(
			"%s: cannot omit %s without changing other frontmatter",
			path, strings.Join(omitted, ", "),
		)
	}
	return out, omitted, nil
}

// headerKeyLines returns the 1-based header lines that declare keys: from a
// key's line up to the next top-level key, or the single line of a flat header.
func headerKeyLines(header string, keys []string) map[int]bool {
	lines := strings.Split(header, "\n")
	drop := map[int]bool{}
	var node yaml.Node
	if yaml.Unmarshal([]byte(header), &node) != nil || len(node.Content) != 1 ||
		node.Content[0].Kind != yaml.MappingNode {
		for i, line := range lines {
			key, _, found := strings.Cut(line, ":")
			if found && slices.Contains(keys, key) {
				drop[i+1] = true
			}
		}
		return drop
	}
	content := node.Content[0].Content
	for i := 0; i+1 < len(content); i += 2 {
		if !slices.Contains(keys, content[i].Value) {
			continue
		}
		end := len(lines) + 1
		if i+2 < len(content) {
			end = content[i+2].Line
		}
		for n := content[i].Line; n < end; n++ {
			drop[n] = true
		}
	}
	return drop
}
