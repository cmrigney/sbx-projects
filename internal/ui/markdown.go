package ui

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

// renderMarkdown lays out the markdown agents write for a w-cell column:
// headings, lists, quotes, rules, fenced code and tables, with inline code,
// bold and links styled. Each source line stays a line; agents rarely rely
// on soft wraps, and joining would merge lines like "Tests: pass".
func renderMarkdown(text string, w int) []Line {
	w = max(w, 8)
	src := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []Line
	blank := func() {
		if len(out) > 0 && len(out[len(out)-1].segs) > 0 {
			out = append(out, L())
		}
	}
	for i := 0; i < len(src); i++ {
		raw := strings.TrimRight(src[i], " \t")
		t := strings.TrimSpace(raw)
		lead := len(raw) - len(strings.TrimLeft(raw, " \t"))
		switch {
		case t == "":
			blank()
		case strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~"):
			fence := t[:3]
			for i++; i < len(src) && !strings.HasPrefix(strings.TrimSpace(src[i]), fence); i++ {
				for _, s := range hardWrap(strings.ReplaceAll(src[i], "\t", "    "), w-2) {
					out = append(out, L(S("  ", cDim), S(s, cCyan)))
				}
			}
		case isTableStart(src, i):
			n := i + 2
			for n < len(src) && strings.Contains(src[n], "|") && strings.TrimSpace(src[n]) != "" {
				n++
			}
			out = append(out, renderTable(src[i:n], w)...)
			i = n - 1
		case hrRe.MatchString(t):
			out = append(out, L(S(strings.Repeat("─", w), cLine)))
		case strings.HasPrefix(t, "#"):
			h := strings.TrimLeft(t, "#")
			if h != "" && h[0] != ' ' {
				out = append(out, wrapRuns(inline(t), w, "", "")...)
				break
			}
			blank()
			runs := inline(strings.TrimSpace(strings.TrimRight(h, "# ")))
			for k := range runs {
				runs[k].bold = true
				if len(t)-len(h) <= 2 && runs[k].f == cFg {
					runs[k].f = cAcc
				}
			}
			out = append(out, wrapRuns(runs, w, "", "")...)
		case strings.HasPrefix(t, ">"):
			q := strings.TrimSpace(strings.TrimLeft(t, "> "))
			for _, l := range wrapRuns(dimRuns(inline(q)), w-2, "", "") {
				out = append(out, L(append([]Seg{S("│ ", cLine)}, l.segs...)...))
			}
		default:
			pad := strings.Repeat(" ", min(lead, 8))
			if m := listRe.FindStringSubmatch(t); m != nil {
				marker, body := m[1], m[2]
				if marker == "-" || marker == "*" || marker == "+" {
					marker = "•"
				}
				if b := strings.TrimPrefix(body, "[ ] "); b != body {
					marker, body = marker+" ☐", b
				} else if b := strings.TrimPrefix(strings.TrimPrefix(body, "[x] "), "[X] "); b != body {
					marker, body = marker+" ☑", b
				}
				first := pad + marker + " "
				out = append(out, wrapRuns(inline(body), w, first, strings.Repeat(" ", strw(first)))...)
				break
			}
			out = append(out, wrapRuns(inline(t), w, pad, pad)...)
		}
	}
	for len(out) > 0 && len(out[len(out)-1].segs) == 0 {
		out = out[:len(out)-1]
	}
	return out
}

var (
	hrRe     = regexp.MustCompile(`^([-*_])( *[-*_]){2,}$`)
	listRe   = regexp.MustCompile(`^([-*+]|\d{1,3}[.)])\s+(.*)$`)
	tableSep = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)
	linkRe   = regexp.MustCompile(`^\[([^\]]+)\]\(([^)\s]+)\)`)
)

func isTableStart(src []string, i int) bool {
	return i+1 < len(src) && strings.Contains(src[i], "|") && tableSep.MatchString(strings.TrimSpace(src[i+1])) &&
		strings.Contains(src[i+1], "-")
}

// run is a span of inline text in one style.
type run struct {
	t    string
	f    string
	bold bool
}

func dimRuns(rs []run) []run {
	for i := range rs {
		if rs[i].f == cFg {
			rs[i].f = cDim
		}
	}
	return rs
}

// inline splits a line into styled runs: `code`, **bold** (or __bold__),
// [text](url) links. Single-star emphasis and ~~strikes~~ lose their marks,
// since the terminal has no italics here.
func inline(s string) []run {
	var out []run
	var cur strings.Builder
	bold := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, run{t: cur.String(), f: cFg, bold: bold})
			cur.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte("\\`*_[]()#|~>-!", s[i+1]) >= 0:
			cur.WriteByte(s[i+1])
			i += 2
		case c == '`':
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			ticks := s[i : i+n]
			end := strings.Index(s[i+n:], ticks)
			if end < 0 {
				cur.WriteString(ticks)
				i += n
				break
			}
			flush()
			out = append(out, run{t: strings.TrimSpace(s[i+n : i+n+end]), f: cCyan, bold: bold})
			i += n + end + n
		case strings.HasPrefix(s[i:], "**") || (strings.HasPrefix(s[i:], "__") && boundary(s, i, 2)):
			flush()
			bold = !bold
			i += 2
		case strings.HasPrefix(s[i:], "~~"):
			i += 2
		case (c == '*' || c == '_') && boundary(s, i, 1):
			i++
		case c == '[':
			m := linkRe.FindStringSubmatch(s[i:])
			if m == nil {
				cur.WriteByte(c)
				i++
				break
			}
			flush()
			for _, r := range inline(m[1]) {
				if r.f == cFg {
					r.f = cAcc
				}
				out = append(out, r)
			}
			if m[2] != m[1] && !strings.HasPrefix(m[2], "#") {
				out = append(out, run{t: " (" + m[2] + ")", f: cDim})
			}
			i += len(m[0])
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// boundary says whether an emphasis mark of length n at i sits at a word
// edge, so snake_case and 2*3 keep their characters.
func boundary(s string, i, n int) bool {
	word := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	before := i == 0 || !word(s[i-1])
	after := i+n >= len(s) || !word(s[i+n])
	if before == after {
		return false
	}
	// An opening mark must be followed by text, a closing one preceded by it.
	if before {
		return i+n < len(s) && s[i+n] != ' '
	}
	return i > 0 && s[i-1] != ' '
}

// wrapRuns fills lines of width w from runs, breaking at spaces. first and
// rest prefix the first and following lines.
func wrapRuns(rs []run, w int, first, rest string) []Line {
	type word struct {
		r     run
		space bool
	}
	var words []word
	space := false
	for _, r := range rs {
		for j, f := range strings.Split(r.t, " ") {
			if j > 0 {
				space = true
			}
			if f == "" {
				continue
			}
			words = append(words, word{run{t: f, f: r.f, bold: r.bold}, space && len(words) > 0})
			space = false
		}
		if strings.HasSuffix(r.t, " ") {
			space = true
		}
	}
	var out []Line
	segs := []Seg{S(first, cDim)}
	width := strw(first)
	room := func() int { return w - strw(first) }
	empty := true
	newLine := func() {
		out = append(out, L(segs...))
		segs, width, empty = []Seg{S(rest, cDim)}, strw(rest), true
		room = func() int { return w - strw(rest) }
	}
	for _, wd := range words {
		t := wd.r.t
		if !empty && width+1+strw(t) > w {
			newLine()
		} else if !empty && wd.space {
			segs = append(segs, Seg{t: " ", f: wd.r.f})
			width++
		}
		for strw(t) > room() {
			if !empty {
				newLine()
			}
			cut := runewidth.Truncate(t, room(), "")
			if cut == "" {
				break
			}
			segs = append(segs, Seg{t: cut, f: wd.r.f, bold: wd.r.bold})
			t = strings.TrimPrefix(t, cut)
			newLine()
		}
		if t != "" {
			segs = append(segs, Seg{t: t, f: wd.r.f, bold: wd.r.bold})
			width += strw(t)
			empty = false
		}
	}
	if !empty || len(out) == 0 {
		out = append(out, L(segs...))
	}
	return out
}

// hardWrap splits s every w cells, keeping its spacing, for code.
func hardWrap(s string, w int) []string {
	if s == "" || w <= 0 {
		return []string{s}
	}
	var out []string
	for strw(s) > w {
		cut := runewidth.Truncate(s, w, "")
		if cut == "" {
			break
		}
		out = append(out, cut)
		s = strings.TrimPrefix(s, cut)
	}
	return append(out, s)
}

func tableCells(line string) []string {
	t := strings.TrimSpace(strings.ReplaceAll(line, `\|`, "\x00"))
	t = strings.TrimSuffix(strings.TrimPrefix(t, "|"), "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(strings.ReplaceAll(cells[i], "\x00", "|"))
	}
	return cells
}

// renderTable draws a GFM table with aligned columns. When the columns
// don't fit, the widest give up room first and their cells wrap; when even
// that leaves columns too narrow to read, each row becomes "Header: value"
// lines instead.
func renderTable(lines []string, w int) []Line {
	head := tableCells(lines[0])
	aligns := tableCells(lines[1])
	n := len(head)
	var rows [][][]run
	for _, l := range append(lines[:1:1], lines[2:]...) {
		cells := tableCells(l)
		row := make([][]run, n)
		for c := 0; c < n; c++ {
			if c < len(cells) {
				row[c] = inline(cells[c])
			}
		}
		rows = append(rows, row)
	}
	for c := range rows[0] {
		for k := range rows[0][c] {
			rows[0][c][k].bold = true
		}
	}
	runsWidth := func(rs []run) int {
		n := 0
		for _, r := range rs {
			n += strw(r.t)
		}
		return n
	}
	nat := make([]int, n)
	for _, row := range rows {
		for c, cell := range row {
			nat[c] = max(nat[c], runsWidth(cell), 1)
		}
	}
	const gap = 3
	avail := w - gap*(n-1)
	if avail < 6*n {
		return stackedTable(rows, w)
	}
	cols := fitColumns(nat, avail)
	sep := S(" │ ", cLine)
	var out []Line
	for r, row := range rows {
		cellLines := make([][]Line, n)
		height := 1
		for c, cell := range row {
			cellLines[c] = wrapRuns(cell, cols[c], "", "")
			height = max(height, len(cellLines[c]))
		}
		for k := 0; k < height; k++ {
			var segs []Seg
			for c := 0; c < n; c++ {
				if c > 0 {
					segs = append(segs, sep)
				}
				var cs []Seg
				if k < len(cellLines[c]) {
					cs = cellLines[c][k].segs
				}
				pad := cols[c] - segsWidth(cs)
				left := 0
				if c < len(aligns) {
					a := aligns[c]
					switch {
					case strings.HasPrefix(a, ":") && strings.HasSuffix(a, ":"):
						left = pad / 2
					case strings.HasSuffix(a, ":"):
						left = pad
					}
				}
				segs = append(segs, S(strings.Repeat(" ", left), cFg))
				segs = append(segs, cs...)
				if c < n-1 {
					segs = append(segs, S(strings.Repeat(" ", pad-left), cFg))
				}
			}
			out = append(out, L(segs...))
		}
		if r == 0 {
			var segs []Seg
			for c := 0; c < n; c++ {
				if c > 0 {
					segs = append(segs, S("─┼─", cLine))
				}
				segs = append(segs, S(strings.Repeat("─", cols[c]), cLine))
			}
			out = append(out, L(segs...))
		}
	}
	return out
}

// fitColumns shares avail cells between columns: those that fit an equal
// share keep their natural width, and the rest split what's left.
func fitColumns(nat []int, avail int) []int {
	cols := make([]int, len(nat))
	sum := 0
	for _, v := range nat {
		sum += v
	}
	if sum <= avail {
		copy(cols, nat)
		return cols
	}
	done := make([]bool, len(nat))
	left, open := avail, len(nat)
	for changed := true; changed && open > 0; {
		changed = false
		share := left / open
		for c, v := range nat {
			if !done[c] && v <= share {
				cols[c], done[c] = v, true
				left -= v
				open--
				changed = true
			}
		}
	}
	for c := range nat {
		if !done[c] {
			cols[c] = left / open
			left -= cols[c]
			open--
		}
	}
	return cols
}

func stackedTable(rows [][][]run, w int) []Line {
	var out []Line
	for r, row := range rows[1:] {
		if r > 0 {
			out = append(out, L())
		}
		for c, cell := range row {
			label := []run{{t: plainRuns(rows[0][c]) + ": ", f: cDim}}
			out = append(out, wrapRuns(append(label, cell...), w, "", "  ")...)
		}
	}
	return out
}

func plainRuns(rs []run) string {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(r.t)
	}
	return b.String()
}
