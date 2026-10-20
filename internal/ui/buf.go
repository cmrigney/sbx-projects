package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

// Colour tokens. Foregrounds and backgrounds share one namespace so a
// segment can say "inv on acc" for an inverted badge.
const (
	cFg    = "fg"
	cDim   = "dim"
	cFaint = "faint"
	cLine  = "line"
	cAcc   = "acc"
	cOk    = "ok"
	cWarn  = "warn"
	cBad   = "bad"
	cCyan  = "cyan"
	// cNew marks unread agent replies; orange so it stays apart from warn.
	cNew = "new"
	cInv = "inv"

	gSel  = "sel"
	gWarn = "warnbg"
	gBad  = "badbg"
	gTerm = "term"
	gAcc  = "acc"
)

type action func() tea.Cmd

type cell struct {
	r    rune
	fg   string
	bg   string
	bold bool
	// cont marks the right half of a double-width rune.
	cont bool
}

type hit struct {
	x, y, w, h int
	fn         action
}

type Buf struct {
	w, h int
	c    [][]cell
	hits []hit
}

func newBuf(w, h int) *Buf {
	b := &Buf{w: w, h: h, c: make([][]cell, h)}
	for y := range b.c {
		row := make([]cell, w)
		for x := range row {
			row[x] = cell{r: ' ', fg: cFg}
		}
		b.c[y] = row
	}
	return b
}

// put writes s at (x, y). An empty bg keeps whatever background is there.
func (b *Buf) put(x, y int, s, fg, bg string, bold bool) int {
	if y < 0 || y >= b.h {
		return x + strw(s)
	}
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if rw == 0 {
			continue
		}
		if x >= 0 && x+rw <= b.w {
			c := &b.c[y][x]
			c.r, c.fg, c.bold, c.cont = r, fg, bold, false
			if bg != "" {
				c.bg = bg
			}
			if rw == 2 {
				n := &b.c[y][x+1]
				n.r, n.cont = ' ', true
				if bg != "" {
					n.bg = bg
				}
			}
		}
		x += rw
	}
	return x
}

func (b *Buf) bg(x, y, w int, bg string) {
	if y < 0 || y >= b.h {
		return
	}
	for i := max(0, x); i < x+w && i < b.w; i++ {
		b.c[y][i].bg = bg
	}
}

func (b *Buf) clear(x, y, w, h int) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			if j >= 0 && j < b.h && i >= 0 && i < b.w {
				b.c[j][i] = cell{r: ' ', fg: cFg}
			}
		}
	}
}

func (b *Buf) hit(x, y, w, h int, fn action) {
	if fn != nil && w > 0 {
		b.hits = append(b.hits, hit{x, y, w, h, fn})
	}
}

func (b *Buf) hitAt(x, y int) action {
	for i := len(b.hits) - 1; i >= 0; i-- {
		h := b.hits[i]
		if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
			return h.fn
		}
	}
	return nil
}

func (b *Buf) box(x, y, w, h int, title string, on bool) {
	if w < 2 || h < 2 {
		return
	}
	f := cLine
	if on {
		f = cAcc
	}
	b.put(x, y, "╭"+strings.Repeat("─", w-2)+"╮", f, "", false)
	for j := 1; j < h-1; j++ {
		b.put(x, y+j, "│", f, "", false)
		b.put(x+w-1, y+j, "│", f, "", false)
	}
	b.put(x, y+h-1, "╰"+strings.Repeat("─", w-2)+"╯", f, "", false)
	if title != "" {
		tf := cFg
		if on {
			tf = cAcc
		}
		b.put(x+2, y, " "+trunc(title, w-6)+" ", tf, "", true)
	}
}

// fade dims everything behind an overlay and drops its click targets.
func (b *Buf) fade() {
	for _, row := range b.c {
		for i := range row {
			row[i].fg, row[i].bg, row[i].bold = cFaint, "", false
		}
	}
	b.hits = nil
}

func (b *Buf) render(t *theme) string {
	var sb strings.Builder
	for y, row := range b.c {
		if y > 0 {
			sb.WriteByte('\n')
		}
		var run strings.Builder
		var key cell
		flush := func() {
			if run.Len() > 0 {
				sb.WriteString(t.style(key.fg, key.bg, key.bold).Render(run.String()))
				run.Reset()
			}
		}
		for i, c := range row {
			if c.cont {
				continue
			}
			k := cell{fg: c.fg, bg: c.bg, bold: c.bold}
			if i == 0 || k != key {
				flush()
				key = k
			}
			run.WriteRune(c.r)
		}
		flush()
	}
	return sb.String()
}

// Seg is a styled run of text inside a Line.
type Seg struct {
	t    string
	f    string
	bold bool
	bg   string
	on   action
}

func S(t, f string) Seg           { return Seg{t: t, f: f} }
func B(t, f string) Seg           { return Seg{t: t, f: f, bold: true} }
func (s Seg) click(fn action) Seg { s.on = fn; return s }
func (s Seg) onBg(bg string) Seg  { s.bg = bg; return s }

type Line struct {
	segs  []Seg
	right []Seg
	bg    string
	on    action
}

func L(segs ...Seg) Line { return Line{segs: segs} }

func (l Line) R(segs ...Seg) Line   { l.right = segs; return l }
func (l Line) G(bg string) Line     { l.bg = bg; return l }
func (l Line) click(fn action) Line { l.on = fn; return l }

func segsWidth(segs []Seg) int {
	n := 0
	for _, s := range segs {
		n += strw(s.t)
	}
	return n
}

// paint draws lines[scroll:] into the w×h area at (x, y). Line
// backgrounds and click targets extend one cell into the padding on each
// side, matching how the selection bar looks in the mockup.
func (b *Buf) paint(x, y, w, h int, lines []Line, scroll int) {
	for i := 0; i < h; i++ {
		li := i + scroll
		if li < 0 || li >= len(lines) {
			break
		}
		l := lines[li]
		yy := y + i
		if l.bg != "" {
			b.bg(x-1, yy, w+2, l.bg)
		}
		if l.on != nil {
			b.hit(x-1, yy, w+2, 1, l.on)
		}
		rw := segsWidth(l.right)
		room := w
		if rw > 0 {
			room = w - rw - 1
		}
		cx := x
		for _, s := range l.segs {
			if room <= 0 {
				break
			}
			t := trunc(s.t, room)
			n := strw(t)
			b.put(cx, yy, t, s.f, s.bg, s.bold)
			b.hit(cx, yy, n, 1, s.on)
			cx += n
			room -= n
		}
		rx := x + w - rw
		for _, s := range l.right {
			b.put(rx, yy, s.t, s.f, s.bg, s.bold)
			b.hit(rx, yy, strw(s.t), 1, s.on)
			rx += strw(s.t)
		}
	}
}

func strw(s string) int { return runewidth.StringWidth(s) }

func trunc(s string, w int) string {
	if strw(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	return runewidth.Truncate(s, w, "…")
}

func truncL(s string, w int) string {
	if strw(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	rs := []rune(s)
	for i := range rs {
		if strw(string(rs[i:])) <= w-1 {
			return "…" + string(rs[i:])
		}
	}
	return "…"
}

func wrap(t string, w int) []string {
	if w <= 0 {
		return []string{t}
	}
	var out []string
	for _, para := range strings.Split(t, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case strw(line)+1+strw(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
			for strw(line) > w {
				cut := runewidth.Truncate(line, w, "")
				if cut == "" {
					break
				}
				out = append(out, cut)
				line = strings.TrimPrefix(line, cut)
			}
		}
		out = append(out, line)
	}
	return out
}

// flow lays pieces left to right, wrapping to new lines at w.
func flow(pieces [][]Seg, w, gap int, indent string) []Line {
	var out []Line
	var row []Seg
	n := 0
	for _, p := range pieces {
		pw := segsWidth(p)
		if len(row) > 0 && n+gap+pw > w {
			out = append(out, L(row...))
			row, n = nil, 0
		}
		if len(row) == 0 {
			if indent != "" {
				row = append(row, S(indent, cFg))
				n = strw(indent)
			}
		} else {
			row = append(row, S(strings.Repeat(" ", gap), cFg))
			n += gap
		}
		row = append(row, p...)
		n += pw
	}
	if len(row) > 0 {
		out = append(out, L(row...))
	}
	return out
}

type keyHint struct {
	key, label string
	fn         action
}

func keyLines(items []keyHint, w int, labelColor string) []Line {
	pieces := make([][]Seg, 0, len(items))
	for _, k := range items {
		pieces = append(pieces, []Seg{B(k.key, cAcc).click(k.fn), S(" "+k.label, labelColor).click(k.fn)})
	}
	return flow(pieces, w, 3, "")
}

type theme struct {
	colors map[string]string
	cache  map[cell]lipgloss.Style
}

var darkPalette = map[string]string{
	"inv": "#121726", "fg": "#D9DEEC", "dim": "#838CA5", "faint": "#3A425C", "line": "#3A4362",
	"acc": "#8F9AFF", "ok": "#5FD39A", "warn": "#F2B54A", "bad": "#F27387", "cyan": "#6CC6E8", "new": "#FF8A4C",
	"sel": "#232B4D", "warnbg": "#35290F", "badbg": "#3A1A24", "term": "#0D111C",
}

var lightPalette = map[string]string{
	"inv": "#FCFCFE", "fg": "#1E2335", "dim": "#69728A", "faint": "#B7BDCB", "line": "#BCC3D2",
	"acc": "#4553C4", "ok": "#1E8A55", "warn": "#9E5C00", "bad": "#C2384B", "cyan": "#1D6F8E", "new": "#C2410C",
	"sel": "#E3E6FA", "warnbg": "#FBEED6", "badbg": "#F9E1E5", "term": "#EEF0F6",
}

func newTheme(dark bool) *theme {
	p := lightPalette
	if dark {
		p = darkPalette
	}
	return &theme{colors: p, cache: map[cell]lipgloss.Style{}}
}

func (t *theme) style(fg, bg string, bold bool) lipgloss.Style {
	k := cell{fg: fg, bg: bg, bold: bold}
	if s, ok := t.cache[k]; ok {
		return s
	}
	s := lipgloss.NewStyle().Bold(bold)
	if c, ok := t.colors[fg]; ok {
		s = s.Foreground(lipgloss.Color(c))
	}
	if c, ok := t.colors[bg]; ok {
		s = s.Background(lipgloss.Color(c))
	}
	t.cache[k] = s
	return s
}
