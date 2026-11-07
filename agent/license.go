package agent

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// MaxCopyrightLines bounds the copyright notice lines a License keeps.
const MaxCopyrightLines = 5

// maxCopyrightLine bounds one kept copyright line, in bytes.
const maxCopyrightLine = 200

// License is a source repository's root license file, as installs attribute
// converted Agents to it.
type License struct {
	// Path is the repository-relative path of the license file.
	Path string
	// Copyright holds its copyright notice lines in file order, trimmed: lines
	// starting with "Copyright", "©" or "(c)", at most MaxCopyrightLines.
	Copyright []string
	// More reports copyright lines beyond Copyright.
	More bool
}

// ParseLicense reads the copyright notice lines of the license file at path.
func ParseLicense(path string, raw []byte) *License {
	license := &License{Path: path}
	raw = bytes.TrimPrefix(raw, []byte("\uFEFF"))
	for line := range bytes.Lines(raw) {
		text := strings.TrimSpace(string(line))
		if !copyrightLine(text) {
			continue
		}
		if len(license.Copyright) == MaxCopyrightLines {
			license.More = true
			break
		}
		license.Copyright = append(license.Copyright, clip(text, maxCopyrightLine))
	}
	return license
}

// copyrightLine reports a copyright notice line: "Copyright …", "© …" or
// "(c) …". Prose that merely mentions copyright starts in lowercase, and
// license templates ("Copyright [yyyy] [name of copyright owner]") are no
// notice of the repository's own.
func copyrightLine(text string) bool {
	notice := strings.HasPrefix(text, "Copyright") || strings.HasPrefix(text, "COPYRIGHT") ||
		strings.HasPrefix(text, "©") || strings.HasPrefix(text, "(c)") ||
		strings.HasPrefix(text, "(C)")
	if !notice {
		return false
	}
	lower := strings.ToLower(text)
	for _, placeholder := range copyrightPlaceholders {
		if strings.Contains(lower, placeholder) {
			return false
		}
	}
	return true
}

// copyrightPlaceholders mark the template notices license texts carry.
var copyrightPlaceholders = []string{
	"yyyy", "<year>", "[year]", "{year}", "name of copyright owner", "<name of author>",
}

// clip shortens text to at most limit bytes on a rune boundary, keeping only
// printable text.
func clip(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, text)
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + "…"
}
