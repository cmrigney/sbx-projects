package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/store"
)

const wideCols = 104

var spinner = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

type agentInfo struct{ key, label string }

var agents = []agentInfo{
	{"claude", "Claude Code"}, {"codex", "Codex"}, {"gemini", "Gemini CLI"}, {"opencode", "opencode"}, {"shell", "Shell"},
}

func agentLabel(k string) string {
	for _, a := range agents {
		if a.key == k {
			return a.label
		}
	}
	if k == "" {
		return "Agent"
	}
	return k
}

func stateColor(s agent.State) string {
	switch s {
	case agent.StateWorking, agent.StateStarting:
		return cOk
	case agent.StateNeedsYou:
		return cWarn
	case agent.StateFailed:
		return cBad
	case agent.StateReady:
		return cAcc
	}
	return cDim
}

func (m *Model) glyph(s agent.State) string {
	switch s {
	case agent.StateWorking, agent.StateStarting:
		return string(spinner[m.spin%len(spinner)])
	case agent.StateNeedsYou:
		return "◆"
	case agent.StateFailed:
		return "✗"
	case agent.StateReady:
		return "✓"
	}
	return "○"
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func agoPhrase(t time.Time) string {
	switch a := ago(t); a {
	case "now":
		return "just now"
	case "—":
		return "a while ago"
	default:
		return a + " ago"
	}
}

func clock(t time.Time) string {
	if time.Since(t) > 20*time.Hour {
		return t.Format("Mon")
	}
	return t.Format("15:04")
}

func (m *Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if m.w == 0 || m.h == 0 {
		v.SetContent("")
		return v
	}
	var b *Buf
	if m.w >= wideCols {
		b = m.drawWide()
	} else {
		b = m.drawNarrow()
	}
	switch {
	case m.confirm != nil:
		m.overlay(b, m.confirm.title, m.confirmLines(min(60, m.w-2)-4), min(60, m.w-2))
	case m.prompt != nil:
		m.overlay(b, m.prompt.title, m.promptLines(min(72, m.w-2)-4), min(72, m.w-2))
	case m.help:
		m.overlay(b, "Keys", m.helpLines(), min(66, m.w-2))
	case m.reply:
		if it, ok := m.selItem(); ok && it.probe != nil && it.probe.Recap != nil {
			w := min(90, m.w-2)
			r := it.probe.Recap
			m.overlayScroll(b, "Last reply from "+it.name()+" · "+agoPhrase(r.When), renderMarkdown(r.Text, w-4), w, &m.rsc)
		}
	case m.diff != nil:
		w := min(140, m.w-2)
		lines := m.diffLines(w - 4)
		if d := m.diff; d.jump != "" && !d.loading {
			for i, f := range d.files {
				if f.Path == d.jump {
					d.sc = d.starts[i]
				}
			}
			d.jump = ""
		}
		m.overlayScroll(b, "Diff of "+m.diff.name, lines, w, &m.diff.sc)
	case m.modal != nil:
		m.overlay(b, "New sandbox", m.modalLines(min(76, m.w-2)-4), min(76, m.w-2))
	}
	m.buf = b
	v.SetContent(b.render(m.theme))
	return v
}

func (m *Model) header(narrow bool) Line {
	p := m.cur()
	left := []Seg{B(" sbx ", cInv).onBg(gAcc)}
	if p != nil {
		left = append(left, B("  "+p.Name, cFg))
		if !narrow {
			left = append(left, S("  "+tildePath(p.Path), cDim))
		}
	} else {
		left = append(left, B("  projects", cFg))
	}
	if m.mock {
		left = append(left, S("  demo data", cWarn))
	}
	n, _, _ := m.runningCount()
	right := []Seg{S(fmt.Sprintf("%d running", n), cOk), S("   ", cFg),
		B("?", cAcc).click(m.toggleHelp), S(" help", cDim).click(m.toggleHelp)}
	return L(left...).R(right...)
}

func (m *Model) toggleHelp() tea.Cmd { m.help = !m.help; return nil }

func tildePath(p string) string {
	if h := expandHome("~"); h != "~" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func (m *Model) dots(p store.Project) []Seg {
	var names []string
	for name, rec := range m.recs {
		if rec.ProjectID == p.ID {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var segs []Seg
	for _, name := range names[:min(5, len(names))] {
		segs = append(segs, S("●", stateColor(m.item(m.recs[name]).status.State)))
	}
	return segs
}

func (m *Model) projLines(w int) []Line {
	var out []Line
	for i, p := range m.projects {
		i, on := i, i == m.P
		marker, nf := "  ", cFg
		if on {
			marker = "▸ "
		}
		l := L(S(marker, cAcc), Seg{t: p.Name, f: nf, bold: on}).R(m.dots(p)...).click(func() tea.Cmd {
			m.setProject(i)
			m.focus = focusProjects
			return nil
		})
		if on && m.focus == focusProjects {
			l = l.G(gSel)
		}
		out = append(out, l)
	}
	if len(m.projects) == 0 {
		for _, s := range wrap("No projects yet.", w) {
			out = append(out, L(S(s, cDim)))
		}
	}
	add := func() tea.Cmd { m.openAddProject(); return nil }
	out = append(out, L(), L(B("A", cAcc).click(add), S(" Add a project", cFg).click(add)))
	if len(m.loose) > 0 {
		out = append(out, L())
		for _, s := range wrap(fmt.Sprintf("%d sandbox%s outside your projects", len(m.loose), plural(len(m.loose), "es")), w) {
			out = append(out, L(S(s, cDim)))
		}
	}
	return out
}

func plural(n int, suffix string) string {
	if n == 1 {
		return ""
	}
	return suffix
}

func (m *Model) hostLines(w int) []Line {
	n, cpus, mem := m.runningCount()
	bw := max(4, w-6-len(fmt.Sprint(cpus, m.hostCPUs))-1)
	on := 0
	if m.hostCPUs > 0 {
		on = min(bw, (bw*cpus+m.hostCPUs/2)/m.hostCPUs)
	}
	memText := fmt.Sprintf("%.0f GiB", float64(mem)/1024)
	if m.hostMemMiB > 0 {
		memText = fmt.Sprintf("%.0f/%.0f GiB", float64(mem)/1024, float64(m.hostMemMiB)/1024)
	}
	return []Line{
		L(B("Allocated", cDim)),
		L(S("CPU ", cDim), S(strings.Repeat("━", on), cAcc), S(strings.Repeat("━", bw-on), cFaint)).R(S(fmt.Sprintf("%d/%d", cpus, m.hostCPUs), cFg)),
		L(S("Memory", cDim)).R(S(memText, cFg)),
		L(S("Running", cDim)).R(S(fmt.Sprint(n), cOk)),
	}
}

func (m *Model) layoutLine() Line {
	p := m.cur()
	f := p.Layout != store.LayoutRepo
	pf := func() tea.Cmd {
		if !f {
			return m.toggleLayout()
		}
		return nil
	}
	ps := func() tea.Cmd {
		if f {
			return m.toggleLayout()
		}
		return nil
	}
	dot := func(on bool) (string, string) {
		if on {
			return "● ", cAcc
		}
		return "○ ", cDim
	}
	d1, c1 := dot(f)
	d2, c2 := dot(!f)
	lc := func(on bool) Seg {
		return Seg{f: map[bool]string{true: cFg, false: cDim}[on], bold: on}
	}
	l1 := lc(f)
	l1.t = "One per feature"
	l2 := lc(!f)
	l2.t = "One for the repo"
	return L(S("Layout  ", cDim), S(d1, c1).click(pf), l1.click(pf), S("   ", cFg), S(d2, c2).click(ps), l2.click(ps))
}

func (m *Model) listLines(w int) ([]Line, int) {
	p := m.cur()
	if p == nil {
		var out []Line
		for _, s := range wrap("Add a repository as a project to start sandboxes for it. Press A.", w) {
			out = append(out, L(S(s, cDim)))
		}
		return out, -1
	}
	out := []Line{m.layoutLine(), L()}
	selIdx := -1
	its := m.items()
	if p.Layout == store.LayoutRepo {
		if len(its) == 0 {
			start := func() tea.Cmd { return m.startRepoSandbox() }
			for _, s := range wrap("One sandbox shares your working tree. You and the agent edit the same files.", w) {
				out = append(out, L(S(s, cDim)))
			}
			out = append(out, L(), L(B("enter", cAcc).click(start), S(" Start it", cFg).click(start)))
		}
		for _, it := range its {
			if it.name() == m.sel {
				selIdx = len(out)
			}
			out = append(out, m.sbxRow(it))
		}
		if len(its) > 0 {
			for _, s := range wrap("Shares your working tree. You and the agent edit the same files.", w) {
				out = append(out, L(S(s, cDim)))
			}
		}
		nf := func() tea.Cmd { m.openNew(); return nil }
		out = append(out, L(), L(B("f", cAcc).click(nf), S(" Branch off a feature sandbox", cFg).click(nf)))
		return out, selIdx
	}
	first := true
	for _, g := range groups {
		var rows []item
		for _, it := range its {
			if inGroup(g, it.status.State) {
				rows = append(rows, it)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if !first {
			out = append(out, L())
		}
		first = false
		gc := cDim
		if g.key == "attention" {
			gc = cWarn
		}
		out = append(out, L(B(g.label, gc)))
		for _, it := range rows {
			if it.name() == m.sel {
				selIdx = len(out)
			}
			out = append(out, m.sbxRow(it))
		}
	}
	if len(its) == 0 {
		for _, s := range wrap("No sandboxes yet. Press n to start one.", w) {
			out = append(out, L(S(s, cDim)))
		}
	}
	return out, selIdx
}

func (m *Model) sbxRow(it item) Line {
	on := it.name() == m.sel
	name := it.name()
	bar := "  "
	if on {
		bar = "▌ "
	}
	var right []Seg
	if it.unread {
		right = append(right, S("● ", cNew))
	}
	right = append(right, S(ago(it.when), cDim))
	l := L(S(bar, cAcc), S(m.glyph(it.status.State)+" ", stateColor(it.status.State)), Seg{t: it.title(), f: cFg, bold: on || it.unread}).
		R(right...).click(func() tea.Cmd { m.selectName(name); return nil })
	if on {
		l = l.G(gSel)
	}
	return l
}

func (m *Model) detailParts(it item, w int) (top []Line, tabs Line, body []Line) {
	for _, s := range wrap(it.title(), w) {
		top = append(top, L(B(s, cFg)))
	}
	sc := stateColor(it.status.State)
	top = append(top, L(S(m.glyph(it.status.State)+" "+it.status.Text, sc), S("   "+agentLabel(it.rec.Agent), cDim)))
	if it.rec.Branch != "" || it.probe != nil {
		segs := []Seg{S(it.rec.Branch, cCyan)}
		if it.probe != nil {
			a, d := it.probe.Totals()
			if a+d > 0 {
				segs = append(segs, S("  +"+fmt.Sprint(a), cOk), S(" −"+fmt.Sprint(d), cBad))
			}
			if n := len(it.probe.Commits); n > 0 {
				segs = append(segs, S(fmt.Sprintf("  %d commit%s", n, plural(n, "s")), cDim))
			}
		}
		top = append(top, L(segs...))
	}

	type tdef struct {
		t     detailTab
		label string
		badge bool
	}
	ts := []tdef{{tabActivity, "Activity", it.status.State == agent.StateNeedsYou}, {tabChanges, "Changes", false}, {tabSetup, "Setup", false}}
	var tsegs []Seg
	for _, t := range ts {
		t := t
		on := t.t == m.tab
		fn := func() tea.Cmd { m.tab, m.dsc = t.t, 0; return nil }
		fc, bg := cDim, ""
		if on {
			fc, bg = cFg, gSel
		}
		tsegs = append(tsegs, Seg{t: " " + t.label, f: fc, bold: on, bg: bg, on: fn})
		if t.badge {
			tsegs = append(tsegs, Seg{t: " ●", f: cWarn, bg: bg, on: fn})
		}
		tsegs = append(tsegs, Seg{t: " ", f: fc, bg: bg, on: fn}, S(" ", cFg))
	}
	tabs = L(tsegs...)

	ws := it.live.Workspace()
	if ws == "" {
		ws = m.projectByID(it.rec.ProjectID).Path
	}
	switch m.tab {
	case tabActivity:
		body = m.activityBody(it, w, ws)
	case tabChanges:
		body = m.changesBody(it, w)
	case tabSetup:
		body = m.setupBody(it, w, ws)
	}
	return top, tabs, body
}

func (m *Model) activityBody(it item, w int, ws string) []Line {
	var body []Line
	block := func(color, bg, head string, lines []string, keys []keyHint) {
		bar := S("┃ ", color)
		body = append(body, L(bar, B(head, cFg)).G(bg))
		for _, l := range lines {
			for _, s := range wrap(l, w-2) {
				body = append(body, L(bar, S(s, color)).G(bg))
			}
		}
		for _, kl := range keyLines(keys, w-2, cFg) {
			kl.segs = append([]Seg{bar}, kl.segs...)
			body = append(body, kl.G(bg))
		}
		body = append(body, L())
	}
	switch it.status.State {
	case agent.StateNeedsYou:
		block(cWarn, gWarn, "The agent is waiting on you", []string{it.status.Text}, []keyHint{{"enter", "Attach to answer", m.attach}})
	case agent.StateFailed:
		block(cBad, gBad, "Something went wrong", []string{it.status.Text}, []keyHint{{"enter", "Attach and take a look", m.attach}})
	case agent.StateIdle:
		if it.running() {
			block(cDim, gTerm, "The agent is not running", []string{"The sandbox is up but nothing is running in it."}, []keyHint{{"enter", "Start the agent and attach", m.attach}})
		}
	}
	if it.probe == nil || len(it.probe.Events) == 0 {
		msg := "No activity yet."
		switch {
		case it.busy != "":
			msg = it.busy + "…"
		case !agent.ReportsActivity(it.rec.Agent) && it.rec.Agent != "":
			msg = agentLabel(it.rec.Agent) + " doesn't report activity. Attach to see what it is doing."
		case !it.running():
			msg = "No activity recorded. Resume the sandbox to start the agent."
		}
		for _, s := range wrap(msg, w) {
			body = append(body, L(S(s, cDim)))
		}
		return body
	}
	for _, f := range agent.Feed(it.probe.Events, ws) {
		fc := cFg
		switch f.Kind {
		case agent.FeedYou:
			fc = cAcc
		case agent.FeedGood:
			fc = cOk
		case agent.FeedWarn:
			fc = cWarn
		case agent.FeedBad:
			fc = cBad
		}
		if f.Path != "" {
			room := w - 7 - strw(f.Text) - 1
			body = append(body, L(S(fmt.Sprintf("%-7s", clock(f.When)), cDim), S(f.Text+" ", fc), S(truncL(f.Path, room), fc)))
			continue
		}
		for i, s := range wrap(f.Text, w-7) {
			t := fmt.Sprintf("%-7s", clock(f.When))
			if i > 0 {
				t = "       "
			}
			body = append(body, L(S(t, cDim), S(s, fc)))
		}
	}
	return body
}

func (m *Model) changesBody(it item, w int) []Line {
	var body []Line
	p := it.probe
	if p == nil || (len(p.Commits) == 0 && len(p.Files) == 0) {
		msg := "No changes yet."
		if p == nil && !it.running() {
			msg = "Nothing recorded. Changes show up once the sandbox has run."
		}
		for _, s := range wrap(msg, w) {
			body = append(body, L(S(s, cDim)))
		}
		return body
	}
	base := p.Base
	if base == "" {
		base = m.projectByID(it.rec.ProjectID).DefaultBranch
	}
	n := len(p.Commits)
	body = append(body, L(B(fmt.Sprintf("%d commit%s on ", n, plural(n, "s")), cDim), S(it.rec.Branch, cCyan), S(" since "+base, cDim)), L())
	for _, c := range p.Commits {
		body = append(body, L(S(c.Hash+" ", cWarn), S(c.Subject, cFg)).R(S(ago(c.When), cDim)))
	}
	if len(p.Files) > 0 {
		if n > 0 {
			body = append(body, L())
		}
		body = append(body, L(B(fmt.Sprintf("%d file%s changed", len(p.Files), plural(len(p.Files), "s")), cDim)))
		for _, f := range p.Files {
			path := f.Path
			show := func() tea.Cmd { return m.openDiff(path) }
			if f.Binary {
				body = append(body, L(S(truncL(f.Path, w-7), cFg)).R(S("binary", cDim)).click(show))
				continue
			}
			a, d := "+"+fmt.Sprint(f.Add), "−"+fmt.Sprint(f.Del)
			body = append(body, L(S(truncL(f.Path, w-strw(a)-strw(d)-2), cFg)).R(S(a, cOk), S(" ", cFg), S(d, cBad)).click(show))
		}
		show := func() tea.Cmd { return m.openDiff("") }
		body = append(body, L(), L(B("d", cAcc).click(show), S(" Show the diff, or click a file", cDim).click(show)))
	}
	if !it.running() {
		body = append(body, L())
		for _, s := range wrap("Last seen "+agoPhrase(it.rec.ProbedAt)+", while the sandbox was running.", w) {
			body = append(body, L(S(s, cDim)))
		}
	}
	return body
}

func (m *Model) diffLines(w int) []Line {
	d := m.diff
	d.starts = nil
	msg, mc := "", cDim
	switch {
	case d.loading:
		msg = "Loading the diff…"
	case d.err != "":
		msg, mc = d.err, cBad
	case len(d.files) == 0:
		msg = "No differences from the base branch right now."
	}
	if msg != "" {
		var out []Line
		for _, s := range wrap(msg, w) {
			out = append(out, L(S(s, mc)))
		}
		return out
	}
	var out []Line
	for i, f := range d.files {
		if i > 0 {
			out = append(out, L())
		}
		d.starts = append(d.starts, len(out))
		add, del := 0, 0
		for _, l := range f.Lines {
			switch {
			case strings.HasPrefix(l, "+"):
				add++
			case strings.HasPrefix(l, "-"):
				del++
			}
		}
		var right []Seg
		if f.Note != "" {
			right = append(right, S(f.Note+"  ", cDim))
		}
		if add+del > 0 {
			right = append(right, S("+"+fmt.Sprint(add), cOk), S(" −"+fmt.Sprint(del), cBad))
		}
		out = append(out, L(B(truncL(f.Path, w-segsWidth(right)-1), cFg)).R(right...).G(gSel))
		for _, l := range f.Lines {
			c := cFg
			switch {
			case strings.HasPrefix(l, "@@"):
				c = cCyan
			case strings.HasPrefix(l, "+"):
				c = cOk
			case strings.HasPrefix(l, "-"):
				c = cBad
			case strings.HasPrefix(l, `\`):
				c = cDim
			}
			out = append(out, L(S(strings.ReplaceAll(l, "\t", "    "), c)))
		}
	}
	return out
}

func (m *Model) setupBody(it item, w int, ws string) []Line {
	code := "Your working tree"
	if it.rec.Clone {
		code = "Private clone"
	}
	res := "Stopped"
	if it.running() {
		// sbx omits cpus when --cpus was unset; the sandbox then gets the host default.
		cpu := "Default CPU"
		if it.live.CPUs > 0 {
			cpu = fmt.Sprintf("%d CPU", it.live.CPUs)
		}
		res = fmt.Sprintf("%s · %s", cpu, memText(it.live.MemoryMiB))
	}
	ports := "none"
	if len(it.live.Ports) > 0 {
		var ps []string
		for _, p := range it.live.Ports {
			ps = append(ps, fmt.Sprintf("%d → %d", p.HostPort, p.SandboxPort))
		}
		ports = strings.Join(ps, ", ")
	}
	created := "—"
	if !it.rec.CreatedAt.IsZero() {
		created = it.rec.CreatedAt.Format("Jan 2 15:04")
	}
	branch := it.rec.Branch
	if branch == "" {
		branch = "—"
	}
	kits := strings.Join(it.rec.Kits, ", ")
	switch {
	case kits == "" && it.rec.Source == "adopted":
		kits = "—"
	case kits == "":
		kits = "none"
	}
	mcps := strings.Join(it.rec.MCPs, ", ")
	switch {
	case mcps == "" && it.rec.Source == "adopted":
		mcps = "—"
	case mcps == "":
		mcps = "none"
	}
	agentName := agentLabel(it.rec.Agent)
	if it.rec.Agent == "claude" && it.rec.ClaudeModel != "" {
		agentName += " (" + core.ModelLabel("claude", it.rec.ClaudeModel) + ")"
	} else if it.rec.Agent == "codex" && it.rec.CodexModel != "" {
		agentName += " (" + core.ModelLabel("codex", it.rec.CodexModel) + ")"
	}
	rows := [][2]string{
		{"Sandbox", it.rec.Name}, {"Agent", agentName}, {"Branch", branch}, {"Code", code},
		{"Kits", kits}, {"MCP", mcps}, {"Workspace", tildePath(ws)}, {"Resources", res}, {"Ports", ports}, {"Created", created},
	}
	var body []Line
	for _, r := range rows {
		for i, s := range wrap(r[1], w-12) {
			k := r[0]
			if i > 0 {
				k = ""
			}
			body = append(body, L(S(fmt.Sprintf("%-12s", k), cDim), S(s, cFg)))
		}
	}
	body = append(body, L())
	for _, s := range wrap(fmt.Sprintf("Attach opens the agent's tmux session with sbx exec -it %s. Press ctrl-b d to come back.", it.rec.Name), w) {
		body = append(body, L(S(s, cDim)))
	}
	return body
}

func memText(mib int64) string {
	if mib <= 0 {
		return "default memory"
	}
	return fmt.Sprintf("%.0f GiB", float64(mib)/1024)
}

func (m *Model) acts(it item) []keyHint {
	var a []keyHint
	if it.busy != "" {
		return a
	}
	switch {
	case it.running() && it.agentUp:
		a = append(a, keyHint{"enter", "Attach", m.attach}, keyHint{"i", "Message", m.openInstruct})
	case it.running():
		a = append(a, keyHint{"enter", "Start agent", m.attach})
	default:
		a = append(a, keyHint{"r", "Resume", func() tea.Cmd { return m.resume(false) }})
	}
	if it.probe != nil && it.probe.Recap != nil {
		label := "Last reply"
		if it.unread {
			label = "New reply"
		}
		a = append(a, keyHint{"t", label, m.openReply})
	}
	if m.tab == tabChanges && it.probe != nil && len(it.probe.Files) > 0 {
		a = append(a, keyHint{"d", "Diff", func() tea.Cmd { return m.openDiff("") }})
	}
	if canPR(it) {
		a = append(a, keyHint{"p", "Open PR", m.openPR})
	}
	if canCheckout(it) {
		a = append(a, keyHint{"c", "Check out locally", m.checkoutLocal})
	}
	if canOpenIDE(it) {
		a = append(a, keyHint{"o", "Open in IDE", m.openIDE})
	}
	if it.running() {
		a = append(a, keyHint{"s", "Stop", m.stop}, keyHint{"$", "Shell", m.shell})
	}
	a = append(a, keyHint{"x", "Remove", m.askRemove})
	return a
}

func (m *Model) hints() []keyHint {
	switch {
	case m.confirm != nil:
		return []keyHint{{"y", m.confirm.yes, m.confirmYes}, {"esc", "Cancel", m.closeOverlay}}
	case m.prompt != nil:
		return []keyHint{{"enter", m.prompt.action, m.submitPrompt}, {"esc", "Cancel", m.closeOverlay}}
	case m.help:
		return []keyHint{{"esc", "Close help", m.closeOverlay}}
	case m.reply:
		return []keyHint{{"j/k", "Scroll", nil}, {"esc", "Close", m.closeOverlay}}
	case m.diff != nil:
		return []keyHint{{"j/k", "Scroll", nil}, {"n/N", "Next or previous file", nil}, {"esc", "Close", m.closeOverlay}}
	case m.modal != nil:
		choose := keyHint{"←→", "Choose", nil}
		switch m.modal.f {
		case fMore:
			choose = keyHint{"space", "Show", func() tea.Cmd { m.modal.expand(); return nil }}
		case fMCP:
			choose = keyHint{"space", "Pick", func() tea.Cmd { m.modal.toggleMCP(m.modal.mcpCur); return nil }}
		}
		return []keyHint{{"tab", "Next field", func() tea.Cmd { m.modal.move(1); return nil }}, choose,
			{"enter", "Create and start", func() tea.Cmd { return m.create(m.modal) }}, {"esc", "Cancel", m.closeOverlay}}
	}
	var h []keyHint
	if it, ok := m.selItem(); ok {
		h = append(h, m.acts(it)...)
	}
	newFn := func() tea.Cmd { m.openNew(); return nil }
	h = append(h, keyHint{"n", "New", newFn}, keyHint{"?", "More keys", m.toggleHelp})
	return h
}

func (m *Model) msgLine() Line {
	if m.listErr != "" && m.msg == "" {
		return L(B("› ", cBad), S("Can't reach sbx: "+m.listErr, cBad))
	}
	if m.msg == "" {
		return L()
	}
	return L(B("› ", m.msgKind), S(m.msg, m.msgKind))
}

func (m *Model) detailBox(b *Buf, x, y, w, h int) {
	it, ok := m.selItem()
	title := "Details"
	if ok {
		title = it.name()
	}
	b.box(x, y, w, h, title, m.focus == focusDetail)
	if !ok {
		return
	}
	iw := w - 4
	top, tabs, body := m.detailParts(it, iw)
	head := append(top, L(), tabs, L(S(strings.Repeat("─", iw), cLine)))
	b.paint(x+2, y+1, iw, len(head), head, 0)
	bh := h - 2 - len(head)
	if bh <= 0 {
		return
	}
	over := len(body) > bh
	if over {
		bh--
		m.dsc = max(0, min(m.dsc, len(body)-bh))
	} else {
		m.dsc = 0
	}
	b.paint(x+2, y+1+len(head), iw, bh, body, m.dsc)
	if over {
		hint := "▴ j/k scroll"
		if m.dsc+bh < len(body) {
			hint = "j/k scroll ▾"
		}
		b.paint(x+2, y+h-2, iw, 1, []Line{L().R(S(hint, cDim))}, 0)
	}
}

func (m *Model) listScroll(lines []Line, selIdx, avail int) int {
	if len(lines) <= avail {
		m.lsc = 0
		return 0
	}
	if selIdx >= 0 {
		if selIdx < m.lsc+1 {
			m.lsc = max(0, selIdx-1)
		}
		if selIdx >= m.lsc+avail-1 {
			m.lsc = selIdx - avail + 2
		}
	}
	m.lsc = max(0, min(m.lsc, len(lines)-avail))
	return m.lsc
}

func (m *Model) drawWide() *Buf {
	W, H := m.w, m.h
	kb := keyLines(m.hints(), W-2, cDim)
	b := newBuf(W, H)
	b.paint(1, 0, W-2, 1, []Line{m.header(false)}, 0)
	y0 := 1
	bh := H - 2 - len(kb)
	lw := 26
	dw := max(46, (W-lw)*42/100)
	mw := W - lw - dw

	b.box(0, y0, lw, bh, "Projects", m.focus == focusProjects)
	pl := m.projLines(lw - 4)
	hl := m.hostLines(lw - 4)
	b.paint(2, y0+1, lw-4, bh-2, pl, 0)
	if hy := y0 + bh - 1 - len(hl); hy > y0+1+len(pl) {
		b.paint(2, hy, lw-4, len(hl), hl, 0)
	}

	title := "Sandboxes"
	if p := m.cur(); p != nil && p.Layout == store.LayoutRepo {
		title = "Sandbox"
	}
	b.box(lw, y0, mw, bh, title, m.focus == focusList)
	ll, si := m.listLines(mw - 4)
	avail := bh - 2
	b.paint(lw+2, y0+1, mw-4, avail, ll, m.listScroll(ll, si, avail))

	m.detailBox(b, lw+mw, y0, dw, bh)

	my := H - 1 - len(kb)
	b.paint(1, my, W-2, 1, []Line{m.msgLine()}, 0)
	b.paint(1, my+1, W-2, len(kb), kb, 0)
	return b
}

func (m *Model) drawNarrow() *Buf {
	W, H := m.w, m.h
	iw := W - 4
	kb := keyLines(m.hints(), W-2, cDim)
	b := newBuf(W, H)
	b.paint(1, 0, W-2, 1, []Line{m.header(true)}, 0)
	y := 1

	prev := func() tea.Cmd { m.setProject(m.P - 1); return nil }
	next := func() tea.Cmd { m.setProject(m.P + 1); return nil }
	var pl Line
	if p := m.cur(); p != nil {
		pl = L(B("◂ ", cAcc).click(prev), B(p.Name, cFg), B(" ▸", cAcc).click(next)).R(m.dots(*p)...)
	} else {
		add := func() tea.Cmd { m.openAddProject(); return nil }
		pl = L(S("No projects yet  ", cDim), B("A", cAcc).click(add), S(" Add one", cFg).click(add))
	}
	b.box(0, y, W, 3, "Project", m.focus == focusProjects)
	b.paint(2, y+1, iw, 1, []Line{pl}, 0)
	y += 3

	rest := H - y - 1 - len(kb)
	ll, si := m.listLines(iw)
	lh := min(len(ll)+2, max(5, rest*2/5))
	title := "Sandboxes"
	if p := m.cur(); p != nil && p.Layout == store.LayoutRepo {
		title = "Sandbox"
	}
	b.box(0, y, W, lh, title, m.focus == focusList)
	b.paint(2, y+1, iw, lh-2, ll, m.listScroll(ll, si, lh-2))
	y += lh

	if dh := rest - lh; dh >= 4 {
		m.detailBox(b, 0, y, W, dh)
	}
	my := H - 1 - len(kb)
	b.paint(1, my, W-2, 1, []Line{m.msgLine()}, 0)
	b.paint(1, my+1, W-2, len(kb), kb, 0)
	return b
}

func (m *Model) overlay(b *Buf, title string, lines []Line, w int) {
	m.overlayScroll(b, title, lines, w, nil)
}

// overlayScroll is overlay for content that may not fit; scroll is clamped
// to it and a hint shows on the bottom border while there's more.
func (m *Model) overlayScroll(b *Buf, title string, lines []Line, w int, scroll *int) {
	h := min(len(lines)+2, b.h-2)
	x := (b.w - w) / 2
	y := max(1, (b.h-h)/2)
	b.fade()
	b.hit(0, 0, b.w, b.h, m.closeOverlay)
	b.hit(x, y, w, h, func() tea.Cmd { return nil })
	b.clear(x, y, w, h)
	b.box(x, y, w, h, title, true)
	off := 0
	if scroll != nil {
		*scroll = max(0, min(*scroll, len(lines)-(h-2)))
		off = *scroll
		if len(lines) > h-2 {
			hint := " j/k scroll ▾ "
			if off+h-2 >= len(lines) {
				hint = " ▴ j/k scroll "
			}
			b.paint(x+w-2-strw(hint), y+h-1, strw(hint), 1, []Line{L(S(hint, cDim))}, 0)
		}
	}
	b.paint(x+2, y+1, w-4, h-2, lines, off)
	kb := keyLines(m.hints(), b.w-2, cDim)
	b.paint(1, b.h-len(kb), b.w-2, len(kb), kb, 0)
}

func (m *Model) closeOverlay() tea.Cmd {
	m.help, m.modal, m.prompt, m.confirm, m.reply, m.diff = false, nil, nil, nil, false, nil
	return nil
}

func (m *Model) helpLines() []Line {
	sec := func(t string, rows [][2]string) []Line {
		out := []Line{L(B(t, cFg))}
		for _, r := range rows {
			out = append(out, L(B(fmt.Sprintf("  %-18s", r[0]), cAcc), S(r[1], cFg)))
		}
		return append(out, L())
	}
	var out []Line
	out = append(out, sec("Moving around", [][2]string{
		{"tab  shift-tab", "Next or previous pane"}, {"j k   ↑ ↓", "Move in a list, scroll details"},
		{"h l   ← →", "Switch detail tabs, or panes"}, {"[ ]", "Previous or next project"}, {"1 2 3", "Activity, Changes, Setup"},
	})...)
	out = append(out, sec("Sandboxes", [][2]string{
		{"enter", "Attach to the agent (starts it if needed)"}, {"n", "New sandbox"}, {"i", "Send the agent a message"},
		{"t", "Read the agent's last reply in full"}, {"d", "Show the full diff (Changes tab)"}, {"p", "Ask the agent to open a draft PR"},
		{"c", "Fetch its branch into your own checkout"}, {"o", "Open its clone in Cursor or VS Code over SSH"},
		{"s  r  x", "Stop, resume, remove"}, {"$", "Open a shell in the sandbox"},
	})...)
	out = append(out, sec("Projects", [][2]string{
		{"A", "Add a project (a git repo)"}, {"D", "Forget the current project"}, {"m", "Layout: per feature or one for the repo"},
		{"K", "Default kits for new sandboxes"}, {"e", "Open the project in your editor"}, {"?  esc", "Show or hide this help"}, {"q", "Quit"},
	})...)
	out = append(out, L(B("Debug log", cFg)))
	log := []rune("off")
	if m.logPath != "" {
		log = []rune(tildePath(m.logPath))
	}
	// Paths have no spaces to wrap at, so they're split to fit the overlay.
	for w := max(10, min(66, m.w-2)-6); len(log) > 0; {
		n := min(w, len(log))
		out = append(out, L(S("  "+string(log[:n]), cDim)))
		log = log[n:]
	}
	return out
}
