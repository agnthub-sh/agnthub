package agent

import (
	"bytes"
	"errors"
	"fmt"
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
