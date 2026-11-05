package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/agnthub-sh/agnthub/platform/env"
)

// Human output is styled only in a color terminal. NO_COLOR, TERM=dumb and
// redirected output get plain text: no escape sequences and no emoji, with no
// ASCII stand-ins for the emoji either; the +, ~ and - action symbols stay because
// they carry the action. Everything under a status line shares one indent: the
// width of the status line's emoji and the space after it, so the text lines up
// (no indent in plain output, which has no emoji). Blocks are separated by one
// blank line. Tables shorten the middle of paths to fit the terminal width and
// stack each row on very narrow terminals; redirected output has no width unless
// COLUMNS sets one, so nothing is shortened there.

// cell is one table cell. Its styles apply after alignment, so escape sequences
// never affect column widths.
type cell struct {
	text  string
	style lipgloss.Style
	// tail follows text, never shortened, in tailStyle: footnote markers.
	tail      string
	tailStyle lipgloss.Style
	// path dims the directory part of text; the file name keeps style.
	path bool
}

func plainCell(text string) cell {
	return cell{text: text, style: lipgloss.NewStyle()}
}

// pathCell is a path cell: the directory dimmed, the file name in the default color.
func pathCell(text string) cell {
	return cell{text: text, style: lipgloss.NewStyle(), path: true}
}

// width is the cell's width in terminal cells.
func (c *cell) width() int {
	return ansi.StringWidth(c.text) + ansi.StringWidth(c.tail)
}

// paint renders text (the cell's text, possibly shortened) and the tail.
func (p *printer) paint(c *cell, text string) string {
	out := c.style.Render(text)
	if c.path {
		if i := strings.LastIndexAny(text, `/\`) + 1; i > 0 {
			out = p.styles.dim.Render(text[:i]) + c.style.Render(text[i:])
		}
	}
	if c.tail != "" {
		out += c.tailStyle.Render(c.tail)
	}
	return out
}

// table is a borderless, left-aligned table.
type table struct {
	headers []string
	rows    [][]cell
	// shrink is the column (a path) shortened in the middle when the table is
	// wider than the terminal; -1 for none.
	shrink int
	// wrap is the column (a reason) wrapped onto continuation lines when the table
	// is wider than the terminal; -1 for none.
	wrap int
	// indent is the table's left margin.
	indent int
}

const (
	tableGap = 2
	// stackedHang indents the second line of a stacked row under its first.
	stackedHang = 4
	// minShrink and minWrap are the narrowest path and reason columns worth
	// aligning; below them each row stacks.
	minShrink = 16
	minWrap   = 24
)

// render prints the table at its indent, fitted to the printer's width.
func (p *printer) render(t *table) {
	widths := make([]int, len(t.headers))
	for i, header := range t.headers {
		widths[i] = ansi.StringWidth(header)
	}
	for _, row := range t.rows {
		for i := range row {
			widths[i] = max(widths[i], row[i].width())
		}
	}
	flex, least := t.shrink, minShrink
	if flex < 0 {
		flex, least = t.wrap, minWrap
	}
	if p.width > 0 && flex >= 0 {
		total := t.indent + tableGap*(len(widths)-1)
		for _, w := range widths {
			total += w
		}
		if total > p.width {
			fitted := widths[flex] - (total - p.width)
			if fitted < least {
				p.stacked(t)
				return
			}
			widths[flex] = fitted
		}
	}
	headers := make([]cell, len(t.headers))
	for i, header := range t.headers {
		headers[i] = cell{text: header, style: p.styles.header}
	}
	p.line(headers, widths, t)
	for _, row := range t.rows {
		p.line(row, widths, t)
	}
}

// line prints one row; a wrapped cell continues on the following lines.
func (p *printer) line(row []cell, widths []int, t *table) {
	texts := make([][]string, len(row))
	lines := 1
	for i := range row {
		text := row[i].text
		switch i {
		case t.shrink:
			text = truncateMiddle(text, widths[i]-ansi.StringWidth(row[i].tail))
		case t.wrap:
			texts[i] = strings.Split(ansi.Wrap(text, widths[i], ""), "\n")
			lines = max(lines, len(texts[i]))
			continue
		}
		texts[i] = []string{text}
	}
	for n := range lines {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", t.indent))
		for i := range row {
			c := row[i]
			text := ""
			if n < len(texts[i]) {
				text = texts[i][n]
			}
			if n > 0 {
				c.tail = ""
			}
			if i > 0 {
				b.WriteString(strings.Repeat(" ", tableGap))
			}
			b.WriteString(p.paint(&c, text))
			if i < len(row)-1 {
				used := ansi.StringWidth(text) + ansi.StringWidth(c.tail)
				b.WriteString(strings.Repeat(" ", max(0, widths[i]-used)))
			}
		}
		fmt.Fprintln(p.w, strings.TrimRight(b.String(), " "))
	}
}

// stacked prints each row on its own lines when the columns cannot fit: the first
// two cells, then the others indented, with the path on the same line while it
// fits and a wrapped reason on lines of its own.
func (p *printer) stacked(t *table) {
	indent := strings.Repeat(" ", t.indent)
	deeper := strings.Repeat(" ", t.indent+stackedHang)
	room := max(p.width-t.indent-stackedHang, minShrink)
	widths := make([]int, len(t.headers))
	for _, row := range t.rows {
		for i := range row {
			widths[i] = max(widths[i], row[i].width())
		}
	}
	for _, row := range t.rows {
		head := make([]string, 0, 2)
		for i := 0; i < len(row) && i < 2; i++ {
			head = append(head, p.paint(&row[i], row[i].text))
		}
		fmt.Fprintln(p.w, indent+strings.Join(head, "  "))
		var rest []string
		used := 0
		for i := 2; i < len(row); i++ {
			if i == t.shrink || i == t.wrap {
				continue
			}
			pad := strings.Repeat(" ", widths[i]-row[i].width())
			rest = append(rest, p.paint(&row[i], row[i].text)+pad)
			used += widths[i] + tableGap
		}
		var own string
		if t.shrink >= 2 && t.shrink < len(row) {
			path := &row[t.shrink]
			if len(rest) > 0 && room-used >= minShrink {
				rest = append(rest, p.paint(path, truncateMiddle(path.text, room-used)))
			} else {
				own = p.paint(path, truncateMiddle(path.text, room))
			}
		}
		if line := strings.TrimRight(strings.Join(rest, "  "), " "); line != "" {
			fmt.Fprintln(p.w, deeper+line)
		}
		if own != "" {
			fmt.Fprintln(p.w, deeper+own)
		}
		if t.wrap >= 2 && t.wrap < len(row) {
			for line := range strings.SplitSeq(ansi.Wrap(row[t.wrap].text, room, ""), "\n") {
				fmt.Fprintln(p.w, deeper+row[t.wrap].style.Render(line))
			}
		}
	}
}

// truncateMiddle shortens text to width cells by replacing its middle with an
// ellipsis. It keeps a path's first directory while that leaves room for the
// end, which names the file.
func truncateMiddle(text string, width int) string {
	total := ansi.StringWidth(text)
	if total <= width {
		return text
	}
	if width < 2 {
		return ansi.Truncate(text, width, "")
	}
	head := (width - 1) * 2 / 5
	if dir := strings.IndexAny(text, `/\`) + 1; dir > 0 && dir <= (width-1)*2/3 {
		head = max(head, dir)
	}
	tail := width - 1 - head
	return ansi.Truncate(text, head, "") + "…" + ansi.TruncateLeft(text, total-tail, "")
}

// paragraph prints text at the content indent, wrapped to the terminal width
// with continuation lines indented hang more. text may carry styles.
func (p *printer) paragraph(hang int, text string) {
	indent := p.indent()
	pad := strings.Repeat(" ", indent)
	room := p.width - indent - hang
	if p.width == 0 || room < minWrap {
		fmt.Fprintln(p.w, pad+text)
		return
	}
	for i, line := range strings.Split(ansi.Wrap(text, room, ""), "\n") {
		if i > 0 {
			line = strings.Repeat(" ", hang) + line
		}
		fmt.Fprintln(p.w, pad+strings.TrimRight(line, " "))
	}
}

// terminalWidth is the width human output fits: the terminal's columns, else
// COLUMNS; 0 (unlimited) for redirected output without COLUMNS.
func terminalWidth(w io.Writer, e env.Env) int {
	if f, ok := w.(interface{ Fd() uintptr }); ok && term.IsTerminal(f.Fd()) {
		if width, _, err := term.GetSize(f.Fd()); err == nil && width > 0 {
			return width
		}
	}
	if width, err := strconv.Atoi(e.Get("COLUMNS")); err == nil && width > 0 {
		return width
	}
	return 0
}

// emoji returns symbol followed by a space in a styled terminal and "" in plain
// output, where emoji widths are unreliable and screen readers spell them out.
func (p *printer) emoji(symbol string) string {
	if p.plain {
		return ""
	}
	return symbol + " "
}

// indent is the content indent under a status line: the measured width of its
// emoji and the space after it, so the text lines up; 0 in plain output.
func (p *printer) indent() int {
	return ansi.StringWidth(p.emoji("📋"))
}

// spacer separates blocks of human output with exactly one blank line: a
// section asks for one, and it is written only before more output, so output
// never starts or ends with a blank line or has two in a row.
type spacer struct {
	w       io.Writer
	started bool
	pending bool
}

func (s *spacer) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if err := s.flush(); err != nil {
		return 0, err
	}
	s.started = true
	return s.w.Write(b)
}

// flush writes a requested blank line now, e.g. before a prompt that writes to
// the terminal directly.
func (s *spacer) flush() error {
	if !s.pending || !s.started {
		s.pending = false
		return nil
	}
	s.pending = false
	_, err := io.WriteString(s.w, "\n")
	return err
}

// section asks for a blank line before the next output.
func (p *printer) section() {
	if p.space != nil {
		p.space.pending = true
	}
}

// gapBeforePrompt writes the blank line between printed output and a prompt.
func (p *printer) gapBeforePrompt() {
	if p.space != nil {
		p.space.pending = true
		_ = p.space.flush()
	}
}

// displayPath shows path relative to the project root when inside it, as ~/...
// under the home directory and absolute otherwise. The home directory is no
// project, so paths there always show as ~/...
func displayPath(root, home, path string) string {
	if path == "" || !filepath.IsAbs(path) {
		return path
	}
	if root != "" && root != home {
		if rel, ok := within(root, path); ok {
			return rel
		}
	}
	if home != "" {
		if rel, ok := within(home, path); ok {
			if rel == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + rel
		}
	}
	return path
}

// within returns path relative to dir when path is dir or below it.
func within(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
