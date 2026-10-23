package ui

import (
	"log/slog"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cmrigney/sbx-projects/internal/applog"
)

// logView shows the latest debug log entries. It follows new entries
// until the user scrolls up, and again once they scroll back down.
type logView struct {
	sc       int
	follow   bool
	errsOnly bool
}

func (l *logView) scroll(d int) {
	if d < 0 {
		l.follow = false
	}
	l.sc = max(0, l.sc+d)
}

func (m *Model) logKey(key string) tea.Cmd {
	l := m.log
	page := max(1, m.h-8)
	switch key {
	case "esc", "q", "L", "shift+l":
		m.log = nil
	case "j", "down":
		l.scroll(1)
	case "k", "up":
		l.scroll(-1)
	case "space", "pgdown", "f":
		l.scroll(page)
	case "b", "pgup":
		l.scroll(-page)
	case "g", "home":
		l.sc, l.follow = 0, false
	case "G", "shift+g", "end":
		l.follow = true
	case "e":
		l.errsOnly, l.follow = !l.errsOnly, true
	}
	return nil
}

func (m *Model) drawLog(b *Buf) {
	l := m.log
	w := min(140, m.w-2)
	title := "Debug log"
	if l.errsOnly {
		title += " · warnings and errors"
	}
	lines := m.logLines(w - 4)
	if l.follow {
		l.sc = 1 << 30
	}
	m.overlayScroll(b, title, lines, w, &l.sc)
	// overlayScroll clamps sc, so it is at the bottom when it can't grow.
	l.follow = l.sc >= len(lines)-(min(len(lines)+2, b.h-2)-2)
}

func (m *Model) logLines(w int) []Line {
	note := func(s string) []Line {
		var out []Line
		for _, t := range wrap(s, w) {
			out = append(out, L(S(t, cDim)))
		}
		return out
	}
	if m.logs == nil {
		return note("Logging is off. Start sbx-projects without --log off to keep a debug log.")
	}
	var out []Line
	for _, t := range chop(tildePath(m.logPath), w) {
		out = append(out, L(S(t, cDim)))
	}
	n := 0
	for _, e := range m.logs() {
		if m.log.errsOnly && e.Level < slog.LevelWarn {
			continue
		}
		n++
		out = append(out, L())
		out = append(out, logEntryLines(e, w)...)
	}
	if n == 0 {
		msg := "Nothing logged yet."
		if m.log.errsOnly {
			msg = "No warnings or errors so far."
		}
		out = append(append(out, L()), note(msg)...)
	}
	return out
}

// logEntryLines shows an entry's fields one to a line under its message,
// with captured output broken back into its own lines.
func logEntryLines(e applog.Entry, w int) []Line {
	lc := cFaint
	switch {
	case e.Level >= slog.LevelError:
		lc = cBad
	case e.Level >= slog.LevelWarn:
		lc = cWarn
	case e.Level >= slog.LevelInfo:
		lc = cCyan
	}
	out := []Line{L(S(e.Time.Format("15:04:05")+"  ", cDim), B(padRight(e.Level.String(), 5)+"  ", lc), B(e.Msg, cFg))}
	const ind = "    "
	for _, a := range e.Attrs {
		v := strings.TrimRight(strings.NewReplacer("\t", "  ", "\r", "").Replace(a.Value), "\n")
		if !strings.Contains(v, "\n") && strw(ind+a.Key+"  "+v) <= w {
			out = append(out, L(S(ind+a.Key+"  ", cDim), S(v, cFg)))
			continue
		}
		out = append(out, L(S(ind+a.Key, cDim)))
		for _, vl := range strings.Split(v, "\n") {
			for _, t := range chop(vl, w-len(ind)-2) {
				out = append(out, L(S(ind+"  "+t, cFg)))
			}
		}
	}
	return out
}

// chop splits s into pieces at most w cells wide, for text with no
// spaces to wrap at, such as paths and command output.
func chop(s string, w int) []string {
	w = max(1, w)
	var out []string
	line, lw := "", 0
	for _, r := range s {
		rw := strw(string(r))
		if lw+rw > w && line != "" {
			out = append(out, line)
			line, lw = "", 0
		}
		line += string(r)
		lw += rw
	}
	return append(out, line)
}

func padRight(s string, n int) string {
	if d := n - strw(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
