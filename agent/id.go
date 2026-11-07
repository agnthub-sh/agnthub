package agent

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// maxIDLen is the longest identifier ValidID accepts.
const maxIDLen = 64

// foldedLetters spells out the Latin letters that have no decomposition.
var foldedLetters = map[rune]string{
	'ß': "ss", 'æ': "ae", 'Æ': "ae", 'ø': "o", 'Ø': "o", 'œ': "oe", 'Œ': "oe",
	'ł': "l", 'Ł': "l", 'đ': "d", 'Đ': "d", 'ð': "d", 'Ð': "d", 'þ': "th", 'Þ': "th",
	'ı': "i",
}

// NormalizeID derives an identifier from a file name stem or a display name:
// ASCII-folded ("Café" → "cafe"), lowercase, with every run of other
// characters ("Search Relevance (v2)") a single '-', underscores kept, and
// separators trimmed from both ends; at most 64 bytes. It is deterministic,
// returns valid identifiers unchanged and returns "" when nothing
// identifier-shaped remains (for example a name written only in CJK).
func NormalizeID(name string) string {
	if ValidID(name) {
		return name
	}
	var b strings.Builder
	pendingDash := false
	write := func(r rune) {
		if pendingDash && b.Len() > 0 {
			b.WriteByte('-')
		}
		pendingDash = false
		b.WriteRune(r)
	}
	for _, r := range norm.NFKD.String(name) {
		switch {
		case r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9'):
			write(r)
		case r >= 'A' && r <= 'Z':
			write(unicode.ToLower(r))
		case r == '_':
			if b.Len() > 0 {
				write(r)
			}
		case unicode.Is(unicode.Mn, r):
			// a combining mark left by decomposition: "é" is "e" + U+0301
		case foldedLetters[r] != "":
			for _, folded := range foldedLetters[r] {
				write(folded)
			}
		default:
			pendingDash = true
		}
	}
	id := strings.TrimRight(b.String(), "_")
	if len(id) > maxIDLen {
		id = id[:maxIDLen]
	}
	id = strings.TrimRight(id, "-_")
	if !ValidID(id) {
		return ""
	}
	return id
}

// FileStem is a file name without its directory and Markdown extension: an
// "<id>.agent.md" file (GitHub Copilot's form) is "<id>".
func FileStem(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	for _, suffix := range []string{".agent.md", ".md", ".toml"} {
		if stem, ok := strings.CutSuffix(name, suffix); ok && stem != "" {
			return stem
		}
	}
	return name
}

// IDFromPath is the identifier a file name implies: NormalizeID of its stem.
func IDFromPath(name string) string {
	return NormalizeID(FileStem(name))
}

// DisplayName reports whether a declared name is a display name rather than
// an identifier: any string that is not a valid identifier ("Search
// Relevance Engineer", "FabricAdmin", "code.reviewer"). Identity then comes
// from the file name, and the name is kept only where a harness shows it.
func DisplayName(name string) bool {
	return strings.TrimSpace(name) != "" && !ValidID(name)
}
