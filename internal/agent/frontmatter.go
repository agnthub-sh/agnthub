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
	text := string(bytes.TrimPrefix(raw, utf8BOM))
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("%s: %w", path, ErrNoFrontmatter)
	}
	rest := text[len("---\n"):]
	var header, body string
	switch {
	case strings.HasPrefix(rest, "---\n"):
		body = rest[len("---\n"):]
	case rest == "---":
	default:
		end := strings.Index(rest, "\n---\n")
		if end < 0 {
			if !strings.HasSuffix(rest, "\n---") {
				return nil, fmt.Errorf("%s: unterminated YAML frontmatter", path)
			}
			end = len(rest) - len("\n---")
			header = rest[:end]
		} else {
			header = rest[:end]
			body = rest[end+len("\n---\n"):]
		}
	}
	fields := map[string]any{}
	if strings.TrimSpace(header) != "" {
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
	}
	return &Document{Path: path, Raw: raw, Frontmatter: fields, Body: body}, nil
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
