package mcpsetup

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Codex keeps MCP servers in config.toml as [mcp_servers.<name>] tables. The
// editor below removes and writes only the agnthub server's tables (and their
// subtables), line by line, so every other line keeps its comments and layout;
// the result is checked by parsing it (see verifyTOML).

// tomlLine is one line of a TOML document and what it is.
type tomlLine struct {
	text string
	// header is the dotted key of a [table] or [[array]] header line, else nil.
	header []string
	// content reports a line that is neither blank nor only a comment.
	content bool
}

// splitTOML splits src into lines, recognizing table headers outside multi-line
// strings and multi-line arrays or inline tables.
func splitTOML(src []byte) []tomlLine {
	text := string(src)
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	lines := make([]tomlLine, 0, len(raw))
	var s tomlScanner
	for _, line := range raw {
		l := tomlLine{text: line}
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if s.normal() && strings.HasPrefix(trimmed, "[") {
			l.header = tableKey(trimmed)
			l.content = true
			lines = append(lines, l)
			continue
		}
		inValue := !s.normal()
		l.content = inValue || (trimmed != "" && !strings.HasPrefix(trimmed, "#"))
		s.scan(line)
		lines = append(lines, l)
	}
	return lines
}

// tomlScanner tracks whether a line ends inside a multi-line string or an
// unclosed array or inline table.
type tomlScanner struct {
	multi string // `"""` or `'''` while inside a multi-line string
	depth int
}

func (s *tomlScanner) normal() bool { return s.multi == "" && s.depth == 0 }

func (s *tomlScanner) scan(line string) {
	for i := 0; i < len(line); i++ {
		if s.multi != "" {
			if strings.HasPrefix(line[i:], s.multi) {
				i += 2
				s.multi = ""
			} else if s.multi == `"""` && line[i] == '\\' {
				i++
			}
			continue
		}
		switch c := line[i]; {
		case c == '#':
			return
		case strings.HasPrefix(line[i:], `"""`), strings.HasPrefix(line[i:], `'''`):
			s.multi = line[i : i+3]
			i += 2
		case c == '"':
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' {
					i++
				}
			}
		case c == '\'':
			for i++; i < len(line) && line[i] != '\''; i++ {
			}
		case c == '[' || c == '{':
			s.depth++
		case c == ']' || c == '}':
			if s.depth > 0 {
				s.depth--
			}
		}
	}
}

// tableKey parses the dotted key of a header line such as
// `[mcp_servers."agnthub".env] # comment`.
func tableKey(line string) []string {
	inner := strings.TrimLeft(line, "[")
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(inner); i++ {
		switch c := inner[i]; c {
		case '"', '\'':
			j := strings.IndexByte(inner[i+1:], c)
			if j < 0 {
				return nil
			}
			part := inner[i+1 : i+1+j]
			if c == '"' {
				var decoded map[string]string
				if toml.Unmarshal([]byte("k = "+inner[i:i+2+j]), &decoded) == nil {
					part = decoded["k"]
				}
			}
			cur.WriteString(part)
			i += j + 1
		case '.':
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		case ']':
			return append(parts, strings.TrimSpace(cur.String()))
		default:
			cur.WriteByte(c)
		}
	}
	return nil
}

// serverSections returns the [start, end) line ranges of the tables that belong
// to the MCP server name: its header and those of its subtables, each through
// its last content line and the blank lines after it. Comments after a table's
// last value stay: they usually introduce the next table.
func serverSections(lines []tomlLine, name string) [][2]int {
	var sections [][2]int
	for i := 0; i < len(lines); i++ {
		h := lines[i].header
		if len(h) < 2 || h[0] != "mcp_servers" || h[1] != name {
			continue
		}
		end := i + 1
		last := i
		for ; end < len(lines) && lines[end].header == nil; end++ {
			if lines[end].content {
				last = end
			}
		}
		stop := last + 1
		for stop < end && strings.TrimSpace(lines[stop].text) == "" {
			stop++
		}
		sections = append(sections, [2]int{i, stop})
		i = end - 1
	}
	return sections
}

var errInlineServer = errors.New("the agnthub server is defined inline (not as an " +
	"[mcp_servers.agnthub] table); edit it by hand")

// tomlSet replaces the server's tables with block (a complete [mcp_servers.name]
// table) at the position of the first one, or appends it.
func tomlSet(src []byte, name string, block string, exists bool) ([]byte, error) {
	lines := splitTOML(src)
	sections := serverSections(lines, name)
	if exists && len(sections) == 0 {
		return nil, errInlineServer
	}
	var out bytes.Buffer
	if len(sections) == 0 {
		trimmed := bytes.TrimRight(src, "\n")
		if len(trimmed) > 0 {
			out.Write(trimmed)
			out.WriteString("\n\n")
		}
		out.WriteString(block)
		return out.Bytes(), nil
	}
	at := 0
	for i, sec := range sections {
		writeLines(&out, lines[at:sec[0]])
		if i == 0 {
			out.WriteString(block)
			if sec[1] < len(lines) {
				out.WriteString("\n")
			}
		}
		at = sec[1]
	}
	writeLines(&out, lines[at:])
	return out.Bytes(), nil
}

// tomlRemove deletes the server's tables.
func tomlRemove(src []byte, name string) ([]byte, error) {
	lines := splitTOML(src)
	sections := serverSections(lines, name)
	if len(sections) == 0 {
		return nil, errInlineServer
	}
	var out bytes.Buffer
	at := 0
	for _, sec := range sections {
		writeLines(&out, lines[at:sec[0]])
		at = sec[1]
	}
	writeLines(&out, lines[at:])
	// Removing the last table leaves the blank line that separated it.
	result := bytes.TrimRight(out.Bytes(), "\n")
	if len(result) == 0 {
		return []byte{}, nil
	}
	return append(result, '\n'), nil
}

func writeLines(out *bytes.Buffer, lines []tomlLine) {
	for _, l := range lines {
		out.WriteString(l.text)
		out.WriteByte('\n')
	}
}

// tomlTable renders [mcp_servers.<name>] with entry's fields: strings, and
// objects of strings as inline tables.
func tomlTable(name string, entry object) (string, error) {
	var b strings.Builder
	b.WriteString("[mcp_servers." + tomlKey(name) + "]\n")
	for _, f := range entry {
		b.WriteString(tomlKey(f.key) + " = ")
		switch v := f.value.(type) {
		case string:
			b.WriteString(tomlString(v))
		case object:
			b.WriteString("{ ")
			for i, inner := range v {
				s, ok := inner.value.(string)
				if !ok {
					return "", fmt.Errorf("unsupported TOML value for %s.%s", f.key, inner.key)
				}
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(tomlKey(inner.key) + " = " + tomlString(s))
			}
			b.WriteString(" }")
		default:
			return "", fmt.Errorf("unsupported TOML value for %s", f.key)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func tomlKey(key string) string {
	for _, c := range key {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' &&
			c != '-' {
			return tomlString(key)
		}
	}
	return key
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", c)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// parseTOML decodes src into generic values.
func parseTOML(src []byte) (map[string]any, error) {
	out := map[string]any{}
	if err := toml.Unmarshal(src, &out); err != nil {
		return nil, err
	}
	return out, nil
}
