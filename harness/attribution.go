package harness

import (
	"bytes"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
)

// Attribution names the original a generated artifact was converted from.
type Attribution struct {
	// Source names the source repository ("github.com/owner/repo",
	// "host/path"), or "" for a local directory.
	Source string
	// Path is the repository-relative path of the converted file.
	Path string
	// Declared is the license the converted file declares itself (the
	// portable core's license field), or "".
	Declared string
	// License is the source's root license file, or nil.
	License *agent.License
}

// unattributedVersion is the last conversion contract whose artifacts carry
// no attribution header (v6 added it).
const unattributedVersion = "v5"

// unattributedVersions are the conversion contracts recorded before
// attributedSince.
var unattributedVersions = []string{"v1", "v2", "v3", "v4", unattributedVersion}

// Unattributed reports whether a lock's converter id predates the attribution
// header, so a restore reproduces its artifacts without one.
func Unattributed(recorded string) bool {
	_, version, ok := strings.Cut(recorded, "/")
	return ok && slices.Contains(unattributedVersions, version)
}

// WithoutAttribution returns a generated artifact as the last converter
// without the attribution header recorded it, for restoring such a lock byte
// for byte.
func WithoutAttribution(target agent.HarnessID, artifact Artifact) Artifact {
	artifact.Converter = string(target) + "/" + unattributedVersion
	return artifact
}

// Attribute starts a generated artifact with comment lines that name its
// original, state that agnthub modified it by converting it for the
// installer's Harness, and repeat the source's copyright notice and license:
// YAML comments at the top of Markdown frontmatter, TOML comments at the top
// of a Codex agent. Harnesses ignore comments, so the agent loads and reads as
// before; the result is validated like any generated artifact.
func Attribute(installer Installer, id string, artifact Artifact, a Attribution) (Artifact, error) {
	header := a.lines(installer.Name())
	var out bytes.Buffer
	out.Grow(len(header) + len(artifact.Content))
	content := artifact.Content
	if installer.ID() != agent.Codex {
		rest, ok := bytes.CutPrefix(content, []byte("---\n"))
		if !ok {
			return Artifact{}, &IncompatibleError{
				Harness: installer.ID(),
				Reason:  "the generated agent has no frontmatter to carry its attribution",
			}
		}
		out.WriteString("---\n")
		content = rest
	}
	out.WriteString(header)
	out.Write(content)
	artifact.Content = out.Bytes()
	return selfCheck(installer, id, artifact)
}

// lines renders the attribution as "# " comment lines.
func (a Attribution) lines(harnessName string) string {
	in := ""
	if a.Source != "" {
		in = " in " + commentText(a.Source)
	}
	var b strings.Builder
	b.WriteString("# Adapted from " + commentText(a.Path) + in + "\n")
	b.WriteString("# Modified by agnthub: converted to the " + harnessName + " agent format\n")
	if a.License != nil {
		for _, line := range a.License.Copyright {
			b.WriteString("# " + commentText(line) + "\n")
		}
		if a.License.More {
			b.WriteString("# (more copyright notices in " + commentText(a.License.Path) + ")\n")
		}
	}
	declared := commentText(a.Declared)
	switch {
	case a.License != nil && declared != "":
		b.WriteString("# License: " + declared + "; see " + commentText(a.License.Path) + in + "\n")
	case a.License != nil:
		b.WriteString("# License: see " + commentText(a.License.Path) + in + "\n")
	case declared != "":
		b.WriteString("# License: " + declared + "\n")
	default:
		b.WriteString("# No license file found" + in + "\n")
	}
	return b.String()
}

// commentText keeps text on one comment line: control characters, which
// could end the comment or are not allowed in it, are dropped.
func commentText(text string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, strings.TrimSpace(text))
}
