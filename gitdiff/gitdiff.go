// Package gitdiff renders file changes as a unified diff in git's extended format,
// which `git apply` accepts: created files come from /dev/null with "new file
// mode", deleted ones go to /dev/null with "deleted file mode", and binary
// content is a "GIT binary patch".
package gitdiff

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1" //nolint:gosec // G505: git names blobs by SHA-1; this is no security use
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Action is what a change does to one file.
type Action string

// Actions.
const (
	Create Action = "create"
	Update Action = "update"
	Delete Action = "delete"
)

// Git file modes.
const (
	ModeRegular    uint32 = 0o100644
	ModeExecutable uint32 = 0o100755
	ModeSymlink    uint32 = 0o120000
)

// File is one changed file. Old is the current content (nil when created), New
// the content the change leaves (nil when deleted); a symbolic link's content is
// its target.
type File struct {
	// Path is slash-separated and relative to the patch's base directory. An
	// absolute path (outside the base) is shown without its leading slash, as git
	// shows paths outside a repository.
	Path    string
	Action  Action
	Old     []byte
	New     []byte
	OldMode uint32
	NewMode uint32
}

// SHA256 is the hex SHA-256 of the content the change writes, or of the deleted
// content for a deletion.
func (f *File) SHA256() string {
	content := f.New
	if f.Action == Delete {
		content = f.Old
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Patch is a set of file changes with leading comment lines. git apply ignores
// any text before the first file header, so comments never change what applies.
type Patch struct {
	// Comments are written first, each as one "# " line.
	Comments []string
	Files    []File
}

// Empty reports a patch without file changes.
func (p *Patch) Empty() bool {
	return len(p.Files) == 0
}

// Standard diff colors, as git uses them.
const (
	colorMeta  = "\x1b[1m"
	colorFrag  = "\x1b[36m"
	colorOld   = "\x1b[31m"
	colorNew   = "\x1b[32m"
	colorReset = "\x1b[m"
)

// String renders the patch without colors.
func (p *Patch) String() string {
	var b strings.Builder
	_ = p.Write(&b, false)
	return b.String()
}

// Write renders the patch, with git's standard colors when color is set.
func (p *Patch) Write(w io.Writer, color bool) error {
	out := &writer{w: bufio.NewWriter(w), color: color}
	for _, comment := range p.Comments {
		for line := range strings.SplitSeq(comment, "\n") {
			out.line("", strings.TrimRight("# "+line, " "))
		}
	}
	for i := range p.Files {
		out.file(&p.Files[i])
	}
	return out.w.Flush()
}

type writer struct {
	w     *bufio.Writer
	color bool
}

// line writes text with an optional color and a newline. A write error sticks to
// the bufio.Writer and is returned by Flush.
func (o *writer) line(color, text string) {
	if o.color && color != "" {
		_, _ = o.w.WriteString(color + text + colorReset + "\n")
		return
	}
	_, _ = o.w.WriteString(text + "\n")
}

func (o *writer) file(f *File) {
	name := strings.TrimPrefix(f.Path, "/")
	oldName, newName := quote("a/"+name), quote("b/"+name)
	o.line(colorMeta, "diff --git "+oldName+" "+newName)
	switch f.Action {
	case Create:
		o.line(colorMeta, "new file mode "+mode(f.NewMode))
		o.line(colorMeta, "index "+zeroOID+".."+blobID(f.New))
		oldName = "/dev/null"
	case Delete:
		o.line(colorMeta, "deleted file mode "+mode(f.OldMode))
		o.line(colorMeta, "index "+blobID(f.Old)+".."+zeroOID)
		newName = "/dev/null"
	default:
		if f.OldMode != f.NewMode {
			o.line(colorMeta, "old mode "+mode(f.OldMode))
			o.line(colorMeta, "new mode "+mode(f.NewMode))
			o.line(colorMeta, "index "+blobID(f.Old)+".."+blobID(f.New))
		} else {
			o.line(colorMeta, "index "+blobID(f.Old)+".."+blobID(f.New)+" "+mode(f.NewMode))
		}
	}
	// An empty file created or deleted, or a mode change alone, has no content lines.
	if bytes.Equal(f.Old, f.New) {
		return
	}
	if binary(f.Old) || binary(f.New) {
		o.binary(f)
		return
	}
	o.line(colorMeta, "--- "+oldName)
	o.line(colorMeta, "+++ "+newName)
	for _, h := range hunks(splitLines(f.Old), splitLines(f.New)) {
		o.line(colorFrag, fmt.Sprintf("@@ -%s +%s @@",
			hunkRange(h.oldStart, h.oldCount), hunkRange(h.newStart, h.newCount)))
		for _, l := range h.lines {
			color := ""
			switch l[0] {
			case '-':
				color = colorOld
			case '+':
				color = colorNew
			}
			text, eol := strings.CutSuffix(l, "\n")
			o.line(color, text)
			if !eol {
				o.line("", `\ No newline at end of file`)
			}
		}
	}
}

// hunkRange formats one side of a hunk header; git omits a count of 1.
func hunkRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

func mode(m uint32) string {
	if m == 0 {
		m = ModeRegular
	}
	return strconv.FormatUint(uint64(m), 8)
}

// zeroOID is git's name for an absent blob.
const zeroOID = "0000000000000000000000000000000000000000"

// blobID is git's object name for content: full, because git apply needs the
// full name to apply a binary patch.
func blobID(content []byte) string {
	h := sha1.New() //nolint:gosec // G401: git's object name, no security use
	h.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00"))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// binary reports content git treats as binary: a NUL byte in its first 8000 bytes.
func binary(content []byte) bool {
	return bytes.IndexByte(content[:min(len(content), 8000)], 0) >= 0
}

// quote returns a path as git writes it: in double quotes with C escapes when it
// holds a quote, a backslash, a control character or a byte outside ASCII.
func quote(name string) string {
	needs := false
	for i := range len(name) {
		if c := name[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			needs = true
			break
		}
	}
	if !needs {
		return name
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(name) {
		switch c := name[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c >= 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// binary writes a "GIT binary patch": the new content, then the old content for
// the reverse direction, each as a zlib-compressed literal in git's base85.
func (o *writer) binary(f *File) {
	o.line(colorMeta, "GIT binary patch")
	o.literal(f.New)
	o.literal(f.Old)
}

func (o *writer) literal(content []byte) {
	var compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	_, _ = z.Write(content)
	_ = z.Close()
	o.line("", "literal "+strconv.Itoa(len(content)))
	data := compressed.Bytes()
	for len(data) > 0 {
		n := min(len(data), 52)
		var prefix byte
		if n <= 26 {
			prefix = byte('A' + n - 1)
		} else {
			prefix = byte('a' + n - 27)
		}
		o.line("", string(prefix)+base85(data[:n]))
		data = data[n:]
	}
	o.line("", "")
}

const base85Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
	"abcdefghijklmnopqrstuvwxyz!#$%&()*+-;<=>?@^_`{|}~"

// base85 encodes data as git does: five characters per four bytes, big-endian,
// the last group padded with zero bytes.
func base85(data []byte) string {
	out := make([]byte, 0, (len(data)+3)/4*5)
	for len(data) > 0 {
		var acc uint32
		for i := range 4 {
			acc <<= 8
			if i < len(data) {
				acc |= uint32(data[i])
			}
		}
		var group [5]byte
		for i := 4; i >= 0; i-- {
			group[i] = base85Alphabet[acc%85]
			acc /= 85
		}
		out = append(out, group[:]...)
		data = data[min(len(data), 4):]
	}
	return string(out)
}
