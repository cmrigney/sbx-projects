package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

type textField struct {
	r   []rune
	pos int
}

func newField(s string) *textField { return &textField{r: []rune(s), pos: len([]rune(s))} }

func (f *textField) String() string { return string(f.r) }

func (f *textField) insert(s string) {
	in := []rune(s)
	f.r = append(f.r[:f.pos], append(in, f.r[f.pos:]...)...)
	f.pos += len(in)
}

// key applies an editing key and reports whether it was consumed.
func (f *textField) key(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "left", "ctrl+b":
		f.pos = max(0, f.pos-1)
	case "right", "ctrl+f":
		f.pos = min(len(f.r), f.pos+1)
	case "home", "ctrl+a":
		f.pos = 0
	case "end", "ctrl+e":
		f.pos = len(f.r)
	case "backspace", "ctrl+h":
		if f.pos > 0 {
			f.r = append(f.r[:f.pos-1], f.r[f.pos:]...)
			f.pos--
		}
	case "delete", "ctrl+d":
		if f.pos < len(f.r) {
			f.r = append(f.r[:f.pos], f.r[f.pos+1:]...)
		}
	case "ctrl+u":
		f.r, f.pos = f.r[f.pos:], 0
	case "ctrl+k":
		f.r = f.r[:f.pos]
	case "ctrl+w", "alt+backspace":
		i := f.pos
		for i > 0 && f.r[i-1] == ' ' {
			i--
		}
		for i > 0 && f.r[i-1] != ' ' {
			i--
		}
		f.r = append(f.r[:i], f.r[f.pos:]...)
		f.pos = i
	default:
		if k.Text != "" && k.Mod&^tea.ModShift == 0 {
			f.insert(k.Text)
			return true
		}
		return false
	}
	return true
}

func (f *textField) line(w int, focused bool, fn action) Line {
	fw := w - 2
	start := 0
	if focused && f.pos >= fw {
		start = f.pos - fw + 1
	}
	end := min(len(f.r), start+fw)
	vis := f.r[start:end]
	bg := gTerm
	if focused {
		bg = gSel
	}
	segs := []Seg{S(" ", cFg)}
	if focused {
		rel := f.pos - start
		cur := " "
		after := ""
		if rel < len(vis) {
			cur = string(vis[rel])
			after = string(vis[rel+1:])
		}
		segs = append(segs, S(string(vis[:rel]), cFg).click(fn), S(cur, cInv).onBg("fg").click(fn), S(after, cFg).click(fn))
	} else {
		segs = append(segs, S(trunc(f.String(), fw), cFg).click(fn))
	}
	return L(segs...).G(bg).click(fn)
}

const (
	fSrc = iota
	fValue
	fAgent
	fWS
	fMore
	fModel
	fKits
	fMCP
	fTask
	fCreate
	fieldCount
)

type newForm struct {
	project *store.Project
	f       int
	src     string
	agent   string
	model   string
	// models keeps each model-capable agent's selection while switching agents.
	models map[string]string
	ws     string
	repo   bool
	// more shows the model, kits and MCP rows, which are folded into a
	// one-line summary until asked for.
	more  bool
	value *textField
	kits  *textField
	task  *textField

	// mcps are the registered MCP servers, listed when the form opens.
	// picked starts as the project's last pick and is trimmed to the
	// servers that still exist once the listing arrives.
	mcps      []sbx.MCPServer
	mcpLoaded bool
	mcpErr    string
	mcpCur    int
	picked    map[string]bool
}

func newSandboxForm(p *store.Project) *newForm {
	f := &newForm{project: p, f: fValue, src: "branch", agent: "claude", model: p.ClaudeModel,
		models: map[string]string{"claude": p.ClaudeModel, "codex": p.CodexModel}, ws: "clone", value: newField(""),
		kits: newField(strings.Join(p.Kits, " ")), task: newField(""), picked: map[string]bool{}}
	for _, n := range p.MCPs {
		f.picked[n] = true
	}
	return f
}

type mcpListMsg struct {
	form *newForm
	list []sbx.MCPServer
	err  error
}

func (m *Model) mcpListCmd(f *newForm) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		list, err := be.MCPServers(ctx)
		return mcpListMsg{f, list, err}
	}
}

func (f *newForm) onMCPList(msg mcpListMsg) {
	f.mcpLoaded = true
	if msg.err != nil {
		f.mcpErr = msg.err.Error()
		return
	}
	f.mcps = msg.list
	for n := range f.picked {
		if !slices.ContainsFunc(f.mcps, func(s sbx.MCPServer) bool { return s.Name == n }) {
			delete(f.picked, n)
		}
	}
	if f.f == fMCP && len(f.mcps) == 0 {
		f.move(1)
	}
}

// pickedMCPs are the servers to pass as --static-mcp. Until the listing
// arrives they are the project's last pick; if it fails, there are none,
// since sbx couldn't list them it can't attach them either.
func (f *newForm) pickedMCPs() []string {
	if !f.mcpLoaded {
		return slices.Clone(f.project.MCPs)
	}
	var out []string
	for _, s := range f.mcps {
		if f.picked[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out
}

func (f *newForm) toggleMCP(i int) {
	if i < 0 || i >= len(f.mcps) {
		return
	}
	f.mcpCur = i
	n := f.mcps[i].Name
	f.picked[n] = !f.picked[n]
}

// parseKits splits kit references on spaces and commas. sbx splits --kit
// values on commas too, so a reference can never contain one.
func parseKits(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

func (m *Model) openNew() tea.Cmd {
	p := m.cur()
	if p == nil {
		m.openAddProject()
		return nil
	}
	m.modal = newSandboxForm(p)
	return m.mcpListCmd(m.modal)
}

// skip reports whether field k has nothing to offer in the form's state.
func (f *newForm) skip(k int) bool {
	switch k {
	case fMore:
		return f.more
	case fModel:
		return !f.more || len(core.ModelsFor(f.agent)) == 0
	case fWS:
		return f.src == "pr"
	case fKits:
		return !f.more
	case fMCP:
		return !f.more || f.mcpLoaded && len(f.mcps) == 0
	}
	return false
}

// expand shows the rows behind the summary and focuses the first one.
func (f *newForm) expand() {
	f.more = true
	f.f = fMore
	f.move(1)
}

// moreLabel names what the summary row stands for.
func (f *newForm) moreLabel() string {
	if len(core.ModelsFor(f.agent)) > 0 {
		return "Model, kits and MCP servers"
	}
	return "Kits and MCP servers"
}

func (f *newForm) moreSummary() []Seg {
	var segs []Seg
	add := func(t string, c string) {
		if len(segs) > 0 {
			segs = append(segs, S(" · ", cDim))
		}
		segs = append(segs, S(t, c))
	}
	if len(core.ModelsFor(f.agent)) > 0 {
		add(core.ModelLabel(f.agent, f.model)+" model", cFg)
	}
	switch n := len(parseKits(f.kits.String())); n {
	case 0:
		add("no kits", cDim)
	case 1:
		add("1 kit", cFg)
	default:
		add(fmt.Sprintf("%d kits", n), cFg)
	}
	switch mcps := f.pickedMCPs(); {
	case f.mcpErr != "":
		add("MCP servers unavailable", cWarn)
	case len(mcps) == 0:
		add("no MCP servers", cDim)
	default:
		add(strings.Join(mcps, ", "), cFg)
	}
	return segs
}

func (f *newForm) move(d int) {
	f.f = (f.f + d + fieldCount) % fieldCount
	for f.skip(f.f) {
		f.f = (f.f + d + fieldCount) % fieldCount
	}
}

func (f *newForm) cycle(d int) {
	switch f.f {
	case fSrc:
		if f.src == "branch" {
			f.src = "pr"
			f.ws = "clone"
		} else {
			f.src = "branch"
		}
	case fAgent:
		i := 0
		for j, a := range agents {
			if a.key == f.agent {
				i = j
			}
		}
		f.setAgent(agents[(i+d+len(agents))%len(agents)].key)
	case fModel:
		models := core.ModelsFor(f.agent)
		i := 0
		for j, c := range models {
			if c.ID == f.model {
				i = j
			}
		}
		f.model = models[(i+d+len(models))%len(models)].ID
		f.models[f.agent] = f.model
	case fWS:
		if f.ws == "clone" {
			f.ws = "direct"
		} else {
			f.ws = "clone"
		}
	case fMCP:
		if len(f.mcps) > 0 {
			f.mcpCur = (f.mcpCur + d + len(f.mcps)) % len(f.mcps)
		}
	}
}

func (f *newForm) setAgent(agent string) {
	if len(core.ModelsFor(f.agent)) > 0 {
		f.models[f.agent] = f.model
	}
	f.agent = agent
	f.model = f.models[agent]
}

func (f *newForm) name() string {
	return core.SandboxName(f.agent, f.project.Name, f.source(), f.value.String())
}

// source is how the sandbox starts: "branch", "pr" or "repo".
func (f *newForm) source() string {
	if f.repo {
		return "repo"
	}
	return f.src
}

func (f *newForm) command() string {
	c := "sbx run -d --name " + f.name()
	if f.ws == "clone" {
		c += " --clone"
	}
	for _, k := range parseKits(f.kits.String()) {
		c += " --kit " + k
	}
	if mcps := f.pickedMCPs(); len(mcps) > 0 {
		c += " --static-mcp " + strings.Join(mcps, ",")
	}
	return c + " " + f.agent + " " + tildePath(f.project.Path)
}

func (m *Model) modalLines(w int) []Line {
	f := m.modal
	var L_ []Line
	foc := func(k int) bool { return f.f == k }
	label := func(t string, k int, extra string) {
		fn := func() tea.Cmd { f.f = k; return nil }
		mk, lc := "  ", cFg
		if foc(k) {
			mk, lc = "› ", cAcc
		}
		segs := []Seg{B(mk, cAcc).click(fn), B(t, lc).click(fn)}
		if extra != "" {
			segs = append(segs, S(extra, cDim).click(fn))
		}
		L_ = append(L_, L(segs...))
	}
	radio := func(opts [][2]string, val string, k int, pick func(string)) {
		var pieces [][]Seg
		for _, o := range opts {
			o := o
			on := o[0] == val
			fn := func() tea.Cmd { pick(o[0]); f.f = k; return nil }
			mark, mc, lc := "( )", cDim, cFg
			if on {
				mark, mc = "(•)", cAcc
			}
			pieces = append(pieces, []Seg{Seg{t: mark, f: mc, bold: on, on: fn}, Seg{t: " " + o[1], f: lc, bold: on, on: fn}})
		}
		L_ = append(L_, flow(pieces, w, 3, "  ")...)
	}

	label("What are you working on?", fSrc, "")
	radio([][2]string{{"branch", "New branch"}, {"pr", "Review a PR"}}, f.src, fSrc, func(k string) {
		f.src = k
		if k == "pr" {
			f.ws = "clone"
		}
	})
	L_ = append(L_, L())
	if f.src == "pr" {
		label("Pull request", fValue, " number or URL")
	} else {
		label("Branch name", fValue, " (optional)")
	}
	L_ = append(L_, f.value.line(w, foc(fValue), func() tea.Cmd { f.f = fValue; return nil }))
	L_ = append(L_, L())
	label("Agent", fAgent, "")
	var aopts [][2]string
	for _, a := range agents {
		aopts = append(aopts, [2]string{a.key, a.label})
	}
	radio(aopts, f.agent, fAgent, f.setAgent)
	L_ = append(L_, L())
	if f.src == "pr" {
		label("How the agent sees your code", fWS, "")
		L_ = append(L_, L(S("  Private clone, so it can check out the PR", cDim)))
	} else {
		label("How the agent sees your code", fWS, "")
		radio([][2]string{{"clone", "Private clone"}, {"direct", "Your working tree"}}, f.ws, fWS, func(k string) { f.ws = k })
	}
	L_ = append(L_, L())
	if !f.more {
		label(f.moreLabel(), fMore, " space to change")
		fn := func() tea.Cmd { f.expand(); return nil }
		segs := []Seg{S("  ", cFg)}
		for _, s := range f.moreSummary() {
			segs = append(segs, s.click(fn))
		}
		L_ = append(L_, L(segs...).click(fn), L())
	} else {
		if models := core.ModelsFor(f.agent); len(models) > 0 {
			label("Model", fModel, "")
			var mopts [][2]string
			for _, c := range models {
				mopts = append(mopts, [2]string{c.ID, c.Label})
			}
			radio(mopts, f.model, fModel, func(k string) { f.model = k; f.models[f.agent] = k })
			L_ = append(L_, L())
		}
		label("Kits", fKits, " mixin references, separated by spaces")
		L_ = append(L_, f.kits.line(w, foc(fKits), func() tea.Cmd { f.f = fKits; return nil }))
		L_ = append(L_, L())
		mcpExtra := ""
		if len(f.mcps) > 0 {
			mcpExtra = " space to pick, ←→ to move"
		}
		label("MCP servers", fMCP, mcpExtra)
		L_ = append(L_, m.mcpLines(w)...)
		L_ = append(L_, L())
	}
	label("First instruction", fTask, " (optional)")
	L_ = append(L_, f.task.line(w, foc(fTask), func() tea.Cmd { f.f = fTask; return nil }))
	L_ = append(L_, L())
	var preview []Line
	for _, s := range wrap("$ "+f.command(), w) {
		preview = append(preview, L(S(s, cDim)))
	}
	if agent.ReportsActivity(f.agent) {
		preview = append(preview, L(S("  then starts "+agentLabel(f.agent)+" in tmux with status hooks", cDim)))
	} else {
		preview = append(preview, L(S("  then starts "+agentLabel(f.agent)+" in tmux", cDim)))
	}
	// The preview is only informational, so it goes first when the
	// terminal is too short to show the buttons below it. The rest is a
	// blank line, the buttons and the overlay's border.
	if len(L_)+len(preview)+4 <= m.h-2 {
		L_ = append(append(L_, preview...), L())
	}
	create := func() tea.Cmd { return m.create(f) }
	cf, cfl := cAcc, cFg
	bg := ""
	if foc(fCreate) {
		cf, cfl, bg = cInv, cInv, gAcc
	}
	L_ = append(L_, L().R(B("esc", cAcc).click(m.closeOverlay), S(" Cancel   ", cFg).click(m.closeOverlay),
		Seg{t: " enter ", f: cf, bold: true, bg: bg, on: create}, Seg{t: "Create and start ", f: cfl, bold: true, bg: bg, on: create}))
	return L_
}

func (m *Model) mcpLines(w int) []Line {
	f := m.modal
	switch {
	case !f.mcpLoaded:
		return []Line{L(S("  Listing registered servers…", cDim))}
	case f.mcpErr != "":
		var out []Line
		for _, s := range wrap("Couldn't list MCP servers: "+f.mcpErr, w-2) {
			out = append(out, L(S("  "+s, cWarn)))
		}
		return out
	case len(f.mcps) == 0:
		return []Line{L(S("  None registered. Add one on your host with sbx mcp add.", cDim))}
	}
	var pieces [][]Seg
	for i, s := range f.mcps {
		i := i
		fn := func() tea.Cmd { f.f = fMCP; f.toggleMCP(i); return nil }
		on := f.picked[s.Name]
		mark, mc, lc := "[ ]", cDim, cFg
		if on {
			mark, mc = "[x]", cAcc
		}
		cur := f.f == fMCP && f.mcpCur == i
		if cur {
			lc = cAcc
		}
		p := []Seg{{t: mark, f: mc, bold: on, on: fn}, {t: " " + s.Name, f: lc, bold: on || cur, on: fn}}
		if !s.Ready() {
			p = append(p, Seg{t: " " + strings.ReplaceAll(s.Status, "-", " "), f: cWarn, on: fn})
		}
		pieces = append(pieces, p)
	}
	out := flow(pieces, w, 3, "  ")
	// Say how to fix the server under the cursor, since it can't connect
	// as it stands.
	if f.f == fMCP && f.mcpCur < len(f.mcps) {
		if s := f.mcps[f.mcpCur]; !s.Ready() && s.NextStep != "" {
			for _, t := range wrap("Run "+s.NextStep+" on your host before the agent can use "+s.Name+".", w-2) {
				out = append(out, L(S("  "+t, cDim)))
			}
		}
	}
	return out
}

// promptForm is a one-field dialog.
type promptForm struct {
	title  string
	label  string
	help   string
	action string
	field  *textField
	submit func(string) tea.Cmd
}

func (m *Model) openAddProject() {
	m.prompt = &promptForm{
		title: "Add a project", label: "Path to a git repository",
		help:   "A project is a repo. Sandboxes you start for it, and any existing sandboxes whose workspace is inside it, show up under it.",
		action: "Add project", field: newField(tildePath(DefaultProjectPath())),
		submit: m.addProject,
	}
}

func (m *Model) openKits() tea.Cmd {
	p := m.cur()
	if p == nil {
		return nil
	}
	id := p.ID
	m.prompt = &promptForm{
		title: "Default kits for " + p.Name, label: "Kit references, separated by spaces",
		help:   "Every new sandbox in this project starts with these mixin kits (sbx run --kit). A reference is a directory, ZIP, git URL or OCI image. You can still change them for one sandbox when you create it. Existing sandboxes keep their kits.",
		action: "Save", field: newField(strings.Join(p.Kits, " ")),
		submit: func(text string) tea.Cmd {
			kits := parseKits(text)
			if err := m.st.SetKits(id, kits); err != nil {
				return m.say("Could not save the kits: "+err.Error(), cBad)
			}
			m.prompt = nil
			_ = m.reload()
			if len(kits) == 0 {
				return m.say("New sandboxes start without extra kits", cOk)
			}
			return m.say(fmt.Sprintf("New sandboxes start with %d kit(s)", len(kits)), cOk)
		},
	}
	return nil
}

func (m *Model) openInstruct() tea.Cmd {
	it, ok := m.selItem()
	if !ok {
		return nil
	}
	m.prompt = &promptForm{
		title: "Message " + it.name(), label: "What should the agent do next?",
		help:   "Typed into the agent's session as if you were attached.",
		action: "Send", field: newField(""),
		submit: func(text string) tea.Cmd {
			if strings.TrimSpace(text) == "" {
				return nil
			}
			m.prompt = nil
			return m.sendPrompt(it, text, "Sent to "+it.name())
		},
	}
	return nil
}

func (m *Model) promptLines(w int) []Line {
	p := m.prompt
	out := []Line{L(B("› ", cAcc), B(p.label, cAcc)), p.field.line(w, true, nil), L()}
	for _, s := range wrap(p.help, w) {
		out = append(out, L(S(s, cDim)))
	}
	out = append(out, L(), L().R(B("esc", cAcc).click(m.closeOverlay), S(" Cancel   ", cFg).click(m.closeOverlay),
		Seg{t: " enter ", f: cInv, bold: true, bg: gAcc, on: m.submitPrompt}, Seg{t: p.action + " ", f: cInv, bold: true, bg: gAcc, on: m.submitPrompt}))
	return out
}

func (m *Model) submitPrompt() tea.Cmd {
	if m.prompt == nil {
		return nil
	}
	return m.prompt.submit(m.prompt.field.String())
}

type confirmBox struct {
	title, body, yes string
	fn               func() tea.Cmd
}

func (m *Model) confirmLines(w int) []Line {
	var out []Line
	for _, s := range wrap(m.confirm.body, w) {
		out = append(out, L(S(s, cFg)))
	}
	return append(out, L(), L().R(B("esc", cAcc).click(m.closeOverlay), S(" Cancel   ", cFg).click(m.closeOverlay),
		Seg{t: " y ", f: cInv, bold: true, bg: "bad", on: m.confirmYes}, Seg{t: m.confirm.yes + " ", f: cInv, bold: true, bg: "bad", on: m.confirmYes}))
}

func (m *Model) confirmYes() tea.Cmd {
	c := m.confirm
	m.confirm = nil
	if c == nil {
		return nil
	}
	return c.fn()
}

func (m *Model) activeField() *textField {
	switch {
	case m.prompt != nil:
		return m.prompt.field
	case m.modal != nil && m.modal.f == fValue:
		return m.modal.value
	case m.modal != nil && m.modal.f == fKits:
		return m.modal.kits
	case m.modal != nil && m.modal.f == fTask:
		return m.modal.task
	}
	return nil
}

func (m *Model) onKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if key == "ctrl+c" {
		return tea.Quit
	}
	switch {
	case m.confirm != nil:
		switch key {
		case "y", "enter":
			return m.confirmYes()
		case "n", "esc", "q":
			m.confirm = nil
		}
		return nil
	case m.prompt != nil:
		switch key {
		case "esc":
			m.prompt = nil
		case "enter":
			return m.submitPrompt()
		default:
			m.prompt.field.key(k)
		}
		return nil
	case m.modal != nil:
		return m.modalKey(k)
	case m.help:
		if key == "esc" || key == "?" || key == "q" {
			m.help = false
		}
		return nil
	case m.diff != nil:
		d := m.diff
		switch key {
		case "esc", "q", "d", "enter":
			m.diff = nil
		case "j", "down":
			d.sc++
		case "k", "up":
			d.sc = max(0, d.sc-1)
		case "space", "pgdown", "f":
			d.sc += max(1, m.h-8)
		case "b", "pgup":
			d.sc = max(0, d.sc-max(1, m.h-8))
		case "g", "home":
			d.sc = 0
		case "G", "shift+g", "end":
			d.sc = 1 << 30
		case "n", "]":
			m.diffFile(1)
		case "N", "shift+n", "[":
			m.diffFile(-1)
		}
		return nil
	case m.reply:
		switch key {
		case "esc", "q", "t", "enter":
			m.reply = false
		case "j", "down":
			m.rsc++
		case "k", "up":
			m.rsc = max(0, m.rsc-1)
		case "space", "pgdown", "f":
			m.rsc += max(1, m.h-8)
		case "b", "pgup":
			m.rsc = max(0, m.rsc-max(1, m.h-8))
		}
		return nil
	}

	it, hasSel := m.selItem()
	switch key {
	case "q":
		return tea.Quit
	case "?":
		m.help = true
	case "tab":
		m.focus = (m.focus + 1) % 3
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
	case "j", "down":
		m.down()
	case "k", "up":
		m.up()
	case "h", "left":
		m.side(-1)
	case "l", "right":
		m.side(1)
	case "[":
		m.setProject(m.P - 1)
	case "]":
		m.setProject(m.P + 1)
	case "1":
		m.tab, m.dsc = tabActivity, 0
	case "2":
		m.tab, m.dsc = tabChanges, 0
	case "3":
		m.tab, m.dsc = tabSetup, 0
	case "enter":
		if m.focus == focusProjects {
			m.focus = focusList
			return nil
		}
		if p := m.cur(); p != nil && p.Layout == store.LayoutRepo && !hasSel {
			return m.startRepoSandbox()
		}
		if hasSel && !it.running() {
			return m.resume(true)
		}
		return m.attach()
	case "n", "f":
		return m.openNew()
	case "i":
		if hasSel && it.busy == "" {
			return m.openInstruct()
		}
	case "t":
		return m.openReply()
	case "d":
		if m.tab == tabChanges {
			return m.openDiff("")
		}
	case "s":
		return m.stop()
	case "r":
		return m.resume(false)
	case "x":
		if hasSel {
			return m.askRemove()
		}
	case "p":
		return m.openPR()
	case "c":
		return m.checkoutLocal()
	case "o":
		return m.openIDE()
	case "$":
		return m.shell()
	case "m":
		return m.toggleLayout()
	case "e":
		return m.openEditor()
	case "A", "shift+a":
		m.openAddProject()
	case "K", "shift+k":
		return m.openKits()
	case "D", "shift+d":
		return m.askRemoveProject()
	case "ctrl+r":
		return m.listCmd()
	}
	return nil
}

func (m *Model) modalKey(k tea.KeyPressMsg) tea.Cmd {
	f := m.modal
	key := k.String()
	radio := f.f == fSrc || f.f == fAgent || f.f == fModel || f.f == fWS || f.f == fMCP
	switch key {
	case "esc":
		m.modal = nil
		return nil
	case "enter":
		if f.src == "pr" && strings.TrimSpace(f.value.String()) == "" {
			f.f = fValue
			return m.say("Enter the pull request number", cWarn)
		}
		return m.create(f)
	case "tab", "down":
		f.move(1)
		return nil
	case "shift+tab", "up":
		f.move(-1)
		return nil
	}
	if f.f == fMore {
		if key == "space" || key == "right" {
			f.expand()
		}
		return nil
	}
	if radio {
		switch {
		case key == "space" && f.f == fMCP:
			f.toggleMCP(f.mcpCur)
		case key == "left":
			f.cycle(-1)
		case key == "right", key == "space":
			f.cycle(1)
		}
		return nil
	}
	if fld := m.activeField(); fld != nil {
		fld.key(k)
	}
	return nil
}

func (m *Model) down() {
	switch m.focus {
	case focusProjects:
		m.setProject(m.P + 1)
	case focusList:
		m.moveSel(1)
	default:
		m.dsc++
	}
}

func (m *Model) up() {
	switch m.focus {
	case focusProjects:
		m.setProject(m.P - 1)
	case focusList:
		m.moveSel(-1)
	default:
		m.dsc = max(0, m.dsc-1)
	}
}

func (m *Model) side(d int) {
	if m.focus == focusDetail {
		if d < 0 && m.tab == tabActivity {
			m.focus = focusList
			return
		}
		m.tab = detailTab((int(m.tab) + d + 3) % 3)
		m.dsc = 0
		return
	}
	m.focus = focusPane((int(m.focus) + d + 3) % 3)
}
