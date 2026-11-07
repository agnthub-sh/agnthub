// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found at https://go.dev/LICENSE.
//
// Adapted from Go's internal/diff: the anchored diff below is that package's
// algorithm, returning hunks instead of formatted text.

package gitdiff

import (
	"sort"
	"strings"
)

// contextLines is the number of unchanged lines around each change, as git's default.
const contextLines = 3

// pair is a pair of line indexes in the old and new text.
type pair struct{ x, y int }

// hunk is one "@@" block: 1-based starts (0 for an empty side) and line counts.
type hunk struct {
	oldStart, oldCount, newStart, newCount int
	// lines start with ' ', '-' or '+'; each keeps its newline, and the file's last
	// line has none when the file does not end in one.
	lines []string
}

// splitLines returns the lines of text, each with its newline; the last line has
// none when text does not end in a newline.
func splitLines(text []byte) []string {
	if len(text) == 0 {
		return nil
	}
	l := strings.SplitAfter(string(text), "\n")
	if l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}

// hunks returns an anchored diff of x and y: the smallest number of "unique"
// lines inserted and removed, where a unique line appears exactly once in both.
// Unique lines anchor the matching regions, which keeps unrelated blank lines and
// closing braces apart, and the algorithm runs in O(n log n) time.
func hunks(x, y []string) []hunk {
	var (
		out   []hunk
		done  pair // emitted up to x[:done.x] and y[:done.y]
		chunk pair // start lines of the current hunk
		count pair // lines from each side in the current hunk
		lines []string
	)
	for _, m := range tgs(x, y) {
		if m.x < done.x {
			// Already handled scanning forward from an earlier match.
			continue
		}
		// Expand the match as far as possible: x[start.x:end.x] == y[start.y:end.y].
		start := m
		for start.x > done.x && start.y > done.y && x[start.x-1] == y[start.y-1] {
			start.x--
			start.y--
		}
		end := m
		for end.x < len(x) && end.y < len(y) && x[end.x] == y[end.y] {
			end.x++
			end.y++
		}
		for _, s := range x[done.x:start.x] {
			lines = append(lines, "-"+s)
			count.x++
		}
		for _, s := range y[done.y:start.y] {
			lines = append(lines, "+"+s)
			count.y++
		}
		// Not at the end with too few common lines: the hunk takes them all and goes on.
		if (end.x < len(x) || end.y < len(y)) &&
			(end.x-start.x < contextLines ||
				(len(lines) > 0 && end.x-start.x < 2*contextLines)) {
			for _, s := range x[start.x:end.x] {
				lines = append(lines, " "+s)
				count.x++
				count.y++
			}
			done = end
			continue
		}
		// End the hunk with common lines for context.
		if len(lines) > 0 {
			n := min(end.x-start.x, contextLines)
			for _, s := range x[start.x : start.x+n] {
				lines = append(lines, " "+s)
				count.x++
				count.y++
			}
			done = pair{start.x + n, start.y + n}
			h := hunk{oldStart: chunk.x, oldCount: count.x, newStart: chunk.y, newCount: count.y}
			// 1-based line numbers; an empty side shows as 0,0.
			if count.x > 0 {
				h.oldStart++
			}
			if count.y > 0 {
				h.newStart++
			}
			h.lines = lines
			out = append(out, h)
			count = pair{}
			lines = nil
		}
		if end.x >= len(x) && end.y >= len(y) {
			break
		}
		chunk = pair{end.x - contextLines, end.y - contextLines}
		for _, s := range x[chunk.x:end.x] {
			lines = append(lines, " "+s)
			count.x++
			count.y++
		}
		done = end
	}
	return out
}

// tgs returns the pairs of indexes of the longest common subsequence of unique
// lines in x and y, where a unique line appears once in x and once in y, with
// sentinels {0,0} and {len(x),len(y)}. The algorithm is Thomas G. Szymanski's
// "A Special Case of the Maximal Common Subsequence Problem" (Princeton TR #170).
func tgs(x, y []string) []pair {
	// Count each string's occurrences as 0, 1 or many: 0, -1, -2 for x and
	// 0, -4, -8 for y, so unique strings end at -1+-4.
	m := make(map[string]int)
	for _, s := range x {
		if c := m[s]; c > -2 {
			m[s] = c - 1
		}
	}
	for _, s := range y {
		if c := m[s]; c > -8 {
			m[s] = c - 4
		}
	}
	// xi and yi are the increasing indexes of unique strings in x and y, and
	// inv[i] the index j with x[xi[i]] == y[yi[j]].
	var xi, yi, inv []int
	for i, s := range y {
		if m[s] == -1+-4 {
			m[s] = len(yi)
			yi = append(yi, i)
		}
	}
	for i, s := range x {
		if j, ok := m[s]; ok && j >= 0 {
			xi = append(xi, i)
			inv = append(inv, j)
		}
	}
	// Algorithm A from Szymanski's paper, with A = J = inv and B = [0, n).
	J := inv
	n := len(xi)
	T := make([]int, n)
	L := make([]int, n)
	for i := range T {
		T[i] = n + 1
	}
	for i := range n {
		k := sort.Search(n, func(k int) bool {
			return T[k] >= J[i]
		})
		T[k] = J[i]
		L[i] = k + 1
	}
	k := 0
	for _, v := range L {
		k = max(k, v)
	}
	seq := make([]pair, 2+k)
	seq[1+k] = pair{len(x), len(y)}
	// The latest index with each length extends the one chosen after it.
	for i := n - 1; i >= 0; i-- {
		if L[i] == k {
			seq[k] = pair{xi[i], yi[J[i]]}
			k--
		}
	}
	seq[0] = pair{0, 0}
	return seq
}
