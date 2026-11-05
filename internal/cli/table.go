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
// they carry the action. Tables shorten the middle of paths to fit the terminal
// width and stack each row on very narrow terminals; redirected output has no
// width unless COLUMNS sets one, so nothing is shortened there.

// cell is one table cell. Its style applies after alignment, so escape sequences
// never affect column widths.
type cell struct {
	text  string
	style lipgloss.Style
}

func plainCell(text string) cell {
	return cell{text: text, style: lipgloss.NewStyle()}
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
}

const (
	tableIndent   = 2
	tableGap      = 2
	stackedIndent = 6
	// minShrink and minWrap are the narrowest path and reason columns worth
	// aligning; below them each row stacks.
	minShrink = 16
	minWrap   = 24
)

// render prints the table at indent, fitted to the printer's width.
func (p *printer) render(t *table) {
	widths := make([]int, len(t.headers))
	for i, header := range t.headers {
		widths[i] = ansi.StringWidth(header)
	}
	for _, row := range t.rows {
		for i := range row {
			widths[i] = max(widths[i], ansi.StringWidth(row[i].text))
		}
	}
	flex, least := t.shrink, minShrink
	if flex < 0 {
		flex, least = t.wrap, minWrap
	}
	if p.width > 0 && flex >= 0 {
		total := tableIndent + tableGap*(len(widths)-1)
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
		headers[i] = cell{text: header, style: p.styles.heading}
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
			text = truncateMiddle(text, widths[i])
		case t.wrap:
			texts[i] = strings.Split(ansi.Wrap(text, widths[i], ""), "\n")
			lines = max(lines, len(texts[i]))
			continue
		}
		texts[i] = []string{text}
	}
	for n := range lines {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", tableIndent))
		for i := range row {
			text := ""
			if n < len(texts[i]) {
				text = texts[i][n]
			}
			if i > 0 {
				b.WriteString(strings.Repeat(" ", tableGap))
			}
			if i == len(row)-1 {
				b.WriteString(row[i].style.Render(text))
				break
			}
			pad := strings.Repeat(" ", max(0, widths[i]-ansi.StringWidth(text)))
			b.WriteString(row[i].style.Render(text) + pad)
		}
		fmt.Fprintln(p.w, strings.TrimRight(b.String(), " "))
	}
}

// stacked prints each row on its own lines when the columns cannot fit: the first
// two cells, then the others indented, with the path on the same line while it
// fits and a wrapped reason on lines of its own.
func (p *printer) stacked(t *table) {
	indent := strings.Repeat(" ", tableIndent)
	deeper := strings.Repeat(" ", stackedIndent)
	room := max(p.width-stackedIndent, minShrink)
	widths := make([]int, len(t.headers))
	for _, row := range t.rows {
		for i := range row {
			widths[i] = max(widths[i], ansi.StringWidth(row[i].text))
		}
	}
	for _, row := range t.rows {
		head := make([]string, 0, 2)
		for i := 0; i < len(row) && i < 2; i++ {
			head = append(head, row[i].style.Render(row[i].text))
		}
		fmt.Fprintln(p.w, indent+strings.Join(head, "  "))
		var rest []string
		used := 0
		for i := 2; i < len(row); i++ {
			if i == t.shrink || i == t.wrap {
				continue
			}
			pad := strings.Repeat(" ", widths[i]-ansi.StringWidth(row[i].text))
			rest = append(rest, row[i].style.Render(row[i].text)+pad)
			used += widths[i] + tableGap
		}
		var own string
		if t.shrink >= 2 && t.shrink < len(row) {
			path := row[t.shrink]
			if len(rest) > 0 && room-used >= minShrink {
				rest = append(rest, path.style.Render(truncateMiddle(path.text, room-used)))
			} else {
				own = path.style.Render(truncateMiddle(path.text, room))
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

// paragraph prints text at indent, wrapped to the terminal width with
// continuation lines indented hang more. text may carry styles.
func (p *printer) paragraph(indent, hang int, text string) {
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
