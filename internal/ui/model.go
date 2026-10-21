// Package ui is the sbx-projects terminal interface.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

const (
	probeEvery = 3 * time.Second
	spinEvery  = 120 * time.Millisecond
	opTimeout  = 15 * time.Minute
)

// listEvery is a variable so tests can poll without waiting.
var listEvery = 3 * time.Second

type focusPane int

const (
	focusProjects focusPane = iota
	focusList
	focusDetail
)

type detailTab int

const (
	tabActivity detailTab = iota
	tabChanges
	tabSetup
)

type Options struct {
	Backend sbx.Backend
	Store   *store.Store
	Mock    bool
	// StartPath selects the project containing it on launch.
	StartPath string
	// HostCPUs and HostMemMiB size the host meter.
	HostCPUs   int
	HostMemMiB int64
}

type Model struct {
	be    sbx.Backend
	st    *store.Store
	svc   *core.Service
	mock  bool
	theme *theme

	w, h int

	projects []store.Project
	recs     map[string]*store.Sandbox
	live     map[string]sbx.Sandbox
	listErr  string
	loose    []sbx.Sandbox

	probes  map[string]*agent.Probe
	probing map[string]bool
	probeAt map[string]time.Time
	busy    map[string]string
	// noProbeUntil holds off probes right after a stop or remove.
	noProbeUntil map[string]time.Time

	hostCPUs   int
	hostMemMiB int64

	P     int
	sel   string
	tab   detailTab
	focus focusPane
	dsc   int
	lsc   int
	spin  int

	help bool
	// reply shows the selected agent's last reply; rsc scrolls it.
	reply   bool
	rsc     int
	diff    *diffView
	modal   *newForm
	prompt  *promptForm
	confirm *confirmBox

	msg     string
	msgKind string
	msgID   int

	buf *Buf
}

func New(o Options) (*Model, error) {
	m := &Model{
		be: o.Backend, st: o.Store, mock: o.Mock, theme: newTheme(true),
		svc:  &core.Service{Backend: o.Backend, Store: o.Store, Demo: o.Mock},
		recs: map[string]*store.Sandbox{}, live: map[string]sbx.Sandbox{},
		probes: map[string]*agent.Probe{}, probing: map[string]bool{}, probeAt: map[string]time.Time{},
		busy: map[string]string{}, noProbeUntil: map[string]time.Time{}, focus: focusList,
		hostCPUs: o.HostCPUs, hostMemMiB: o.HostMemMiB,
	}
	if m.hostCPUs == 0 {
		m.hostCPUs = runtime.NumCPU()
	}
	if err := m.reload(); err != nil {
		return nil, err
	}
	if p := m.projectFor(o.StartPath); p != nil {
		for i := range m.projects {
			if m.projects[i].ID == p.ID {
				m.P = i
			}
		}
	}
	return m, nil
}

func (m *Model) reload() error {
	ps, err := m.st.Projects()
	if err != nil {
		return err
	}
	m.projects = ps
	if m.P >= len(ps) {
		m.P = max(0, len(ps)-1)
	}
	recs, err := m.st.Sandboxes()
	if err != nil {
		return err
	}
	m.recs = map[string]*store.Sandbox{}
	for i := range recs {
		r := recs[i]
		m.recs[r.Name] = &r
		if _, ok := m.probes[r.Name]; !ok && r.LastProbe != "" {
			p := agent.ParseProbe(r.LastProbe)
			// A cached probe describes the past; never let it claim the
			// agent is up right now.
			p.AgentUp = false
			m.probes[r.Name] = &p
		}
	}
	return nil
}

func (m *Model) cur() *store.Project {
	if m.P < 0 || m.P >= len(m.projects) {
		return nil
	}
	return &m.projects[m.P]
}

type (
	listMsg struct {
		sbxs []sbx.Sandbox
		err  error
	}
	probeMsg struct {
		name string
		raw  string
		err  error
	}
	opMsg struct {
		name  string
		step  string
		err   error
		out   string
		apply func(out string)
		next  func(out string) tea.Cmd
		done  string
	}
	diffMsg struct {
		name string
		raw  string
		err  error
	}
	execDoneMsg struct {
		name string
		err  error
		// attached is set when the session was the agent's, so anything
		// it said meanwhile has been seen.
		attached bool
	}
	listTick struct{}
	spinTick struct{}
	clearMsg struct{ id int }
)

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.listCmd(), tick(spinEvery, spinTick{}), tea.RequestBackgroundColor)
}

func tick(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

func (m *Model) listCmd() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := be.List(ctx)
		return listMsg{s, err}
	}
}

func (m *Model) probeCmd(name, defBranch string) tea.Cmd {
	be := m.be
	m.probing[name] = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		raw, err := be.Exec(ctx, name, agent.ProbeScript(defBranch))
		return probeMsg{name, raw, err}
	}
}

// run runs an action's steps one after another. Each step's busy text
// shows in the list while it runs. After the last step, then, when set,
// takes over; otherwise the action finishes and says done.
func (m *Model) run(name string, steps []core.Step, then func(out string) tea.Cmd, done string) tea.Cmd {
	st := steps[0]
	next := then
	if len(steps) > 1 {
		rest := steps[1:]
		next = func(string) tea.Cmd { return m.run(name, rest, then, done) }
	}
	m.busy[name] = st.Busy
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		out, err := st.Run(ctx)
		return opMsg{name: name, step: st.Verb, err: err, out: out, apply: st.Apply, next: next, done: done}
	}
}

func (m *Model) say(text, kind string) tea.Cmd {
	m.msg, m.msgKind = text, kind
	m.msgID++
	id := m.msgID
	return tick(5*time.Second, clearMsg{id})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.theme = newTheme(msg.IsDark())
	case spinTick:
		m.spin++
		return m, tick(spinEvery, spinTick{})
	case listTick:
		return m, m.listCmd()
	case clearMsg:
		if msg.id == m.msgID {
			m.msg = ""
		}
	case listMsg:
		return m, tea.Batch(m.onList(msg), tick(listEvery, listTick{}))
	case probeMsg:
		m.onProbe(msg)
	case opMsg:
		return m, m.onOp(msg)
	case diffMsg:
		m.onDiff(msg)
	case mcpListMsg:
		msg.form.onMCPList(msg)
	case execDoneMsg:
		if msg.attached {
			m.markSeen(msg.name)
		}
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, m.say("Session ended: "+msg.err.Error(), cWarn))
		}
		if p := m.cur(); p != nil && msg.name != "" {
			cmds = append(cmds, m.probeCmd(msg.name, p.DefaultBranch))
		}
		return m, tea.Batch(append(cmds, m.listCmd())...)
	case tea.KeyPressMsg:
		return m, m.onKey(msg)
	case tea.PasteMsg:
		if f := m.activeField(); f != nil {
			f.insert(strings.ReplaceAll(msg.Content, "\n", " "))
		}
	case tea.MouseClickMsg:
		if m.buf != nil {
			mo := msg.Mouse()
			if mo.Button == tea.MouseLeft {
				if fn := m.buf.hitAt(mo.X, mo.Y); fn != nil {
					return m, fn()
				}
			}
		}
	case tea.MouseWheelMsg:
		mo := msg.Mouse()
		d := 1
		if mo.Button == tea.MouseWheelUp {
			d = -1
		}
		if m.diff != nil {
			m.diff.sc = max(0, m.diff.sc+d)
		} else if m.reply {
			m.rsc = max(0, m.rsc+d)
		} else if m.focus == focusDetail || mo.X > m.w*2/3 {
			m.dsc = max(0, m.dsc+d)
		} else {
			m.moveSel(d)
		}
	}
	return m, nil
}

func (m *Model) onList(msg listMsg) tea.Cmd {
	if msg.err != nil {
		if errors.Is(msg.err, exec.ErrNotFound) {
			m.listErr = "sbx is not on your PATH"
		} else {
			m.listErr = msg.err.Error()
		}
		return nil
	}
	m.listErr = ""
	m.live = map[string]sbx.Sandbox{}
	for _, s := range msg.sbxs {
		m.live[s.Name] = s
	}
	r := m.svc.Reconcile(m.projects, m.recs, msg.sbxs, func(name string) bool { return m.busy[name] != "" })
	for _, name := range r.Removed {
		delete(m.recs, name)
		delete(m.probes, name)
	}
	m.loose = r.Loose
	if r.Adopted > 0 {
		_ = m.reload()
	}
	m.fixSel()

	var cmds []tea.Cmd
	now := time.Now()
	for name, rec := range m.recs {
		s, ok := m.live[name]
		if !ok || !s.Running() || m.probing[name] || m.busy[name] != "" || now.Before(m.noProbeUntil[name]) {
			continue
		}
		if now.Sub(m.probeAt[name]) < probeEvery-500*time.Millisecond {
			continue
		}
		cmds = append(cmds, m.probeCmd(name, m.projectByID(rec.ProjectID).DefaultBranch))
	}
	return tea.Batch(cmds...)
}

func (m *Model) onProbe(msg probeMsg) {
	m.probing[msg.name] = false
	m.probeAt[msg.name] = time.Now()
	if msg.err != nil {
		return
	}
	p := agent.ParseProbe(msg.raw)
	m.probes[msg.name] = &p
	if rec := m.recs[msg.name]; rec != nil {
		m.svc.SaveProbe(rec, msg.raw, p)
		if it, ok := m.selItem(); ok && m.reply && it.name() == msg.name && core.Unread(rec, &p) {
			m.markSeen(msg.name)
		}
	}
}

func (m *Model) onOp(msg opMsg) tea.Cmd {
	if msg.err != nil {
		delete(m.busy, msg.name)
		return tea.Batch(m.say(fmt.Sprintf("%s %s failed: %s", msg.step, msg.name, msg.err), cBad), m.listCmd())
	}
	if msg.apply != nil {
		msg.apply(msg.out)
	}
	if msg.next != nil {
		return msg.next(msg.out)
	}
	delete(m.busy, msg.name)
	var cmds []tea.Cmd
	if msg.done != "" {
		cmds = append(cmds, m.say(msg.done, cOk))
	}
	cmds = append(cmds, m.listCmd())
	switch msg.step {
	case "Stopping", "Removing":
		// A probe now could land on the sandbox while it is going away,
		// and sbx exec would start it again.
		m.noProbeUntil[msg.name] = time.Now().Add(2 * listEvery)
		if msg.step == "Removing" {
			delete(m.recs, msg.name)
			delete(m.probes, msg.name)
		}
	default:
		if rec := m.recs[msg.name]; rec != nil {
			cmds = append(cmds, m.probeCmd(msg.name, m.projectByID(rec.ProjectID).DefaultBranch))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) projectFor(ws string) *store.Project { return core.ProjectFor(m.projects, ws) }

func (m *Model) projectByID(id int64) store.Project { return core.ProjectByID(m.projects, id) }

// item is everything the views need about one sandbox.
type item struct {
	rec     *store.Sandbox
	live    sbx.Sandbox
	listed  bool
	probe   *agent.Probe
	status  agent.Status
	busy    string
	when    time.Time
	agentUp bool
	// unread is set when the agent has replied since the user last looked.
	unread bool
}

func (it item) name() string  { return it.rec.Name }
func (it item) running() bool { return it.listed && it.live.Running() }

func (it item) title() string {
	if it.rec.Title != "" {
		return it.rec.Title
	}
	return it.rec.Name
}

func (m *Model) item(rec *store.Sandbox) item {
	it := item{rec: rec, probe: m.probes[rec.Name], busy: m.busy[rec.Name]}
	it.live, it.listed = m.live[rec.Name]
	ws := it.live.Workspace()
	if ws == "" {
		ws = m.projectByID(rec.ProjectID).Path
	}
	probe := it.probe
	if !it.running() {
		probe = nil
	} else if probe != nil {
		it.agentUp = probe.AgentUp
	}
	it.status = agent.Derive(it.running(), probe, rec.Agent, ws)
	it.unread = core.Unread(rec, it.probe)
	if it.busy != "" {
		it.status = agent.Status{State: agent.StateStarting, Text: it.busy}
	}
	if it.probe != nil && len(it.probe.Events) > 0 {
		it.when = it.probe.Events[len(it.probe.Events)-1].Time()
	}
	if it.live.LastUsedAt != nil && it.live.LastUsedAt.After(it.when) {
		it.when = *it.live.LastUsedAt
	}
	if it.when.IsZero() {
		it.when = rec.CreatedAt
	}
	return it
}

func (m *Model) items() []item {
	p := m.cur()
	if p == nil {
		return nil
	}
	var out []item
	for _, rec := range m.recs {
		if rec.ProjectID != p.ID {
			continue
		}
		if p.Layout == store.LayoutRepo && rec.Clone {
			continue
		}
		out = append(out, m.item(rec))
	}
	// Order by fields that never change so rows hold still while activity
	// times tick and m.recs (a map) iterates in random order.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].rec, out[j].rec
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.Name < b.Name
	})
	return out
}

type group struct {
	key, label string
	states     []agent.State
}

var groups = []group{
	{"attention", "Needs you", []agent.State{agent.StateNeedsYou, agent.StateFailed}},
	{"working", "Working", []agent.State{agent.StateWorking, agent.StateStarting}},
	{"review", "Ready for you", []agent.State{agent.StateReady}},
	{"parked", "Parked", []agent.State{agent.StateIdle, agent.StateStopped}},
}

func inGroup(g group, s agent.State) bool {
	for _, x := range g.states {
		if x == s {
			return true
		}
	}
	return false
}

func (m *Model) ordered() []item {
	its := m.items()
	var out []item
	for _, g := range groups {
		for _, it := range its {
			if inGroup(g, it.status.State) {
				out = append(out, it)
			}
		}
	}
	return out
}

func (m *Model) selItem() (item, bool) {
	o := m.ordered()
	for _, it := range o {
		if it.name() == m.sel {
			return it, true
		}
	}
	if len(o) > 0 {
		return o[0], true
	}
	return item{}, false
}

func (m *Model) fixSel() {
	if it, ok := m.selItem(); ok {
		m.sel = it.name()
	} else {
		m.sel = ""
	}
}

func (m *Model) setProject(i int) {
	if len(m.projects) == 0 {
		return
	}
	m.P = (i + len(m.projects)) % len(m.projects)
	m.sel, m.tab, m.dsc, m.lsc = "", tabActivity, 0, 0
	m.fixSel()
}

func (m *Model) selectName(name string) {
	m.sel, m.focus, m.dsc = name, focusList, 0
}

func (m *Model) moveSel(d int) {
	o := m.ordered()
	if len(o) == 0 {
		return
	}
	i := 0
	for j, it := range o {
		if it.name() == m.sel {
			i = j
		}
	}
	i = max(0, min(len(o)-1, i+d))
	m.selectName(o[i].name())
}

func (m *Model) runningCount() (n, cpus int, mem int64) {
	for _, s := range m.live {
		if s.Running() {
			n++
			cpus += s.CPUs
			mem += s.MemoryMiB
		}
	}
	return
}

// Actions.

func (m *Model) attachCmd(name string) tea.Cmd {
	m.markSeen(name)
	return tea.ExecProcess(m.be.Interactive(name, agent.AttachArgv()...), func(err error) tea.Msg {
		return execDoneMsg{name: name, err: err, attached: true}
	})
}

// markSeen clears the sandbox's unread mark.
func (m *Model) markSeen(name string) {
	if rec := m.recs[name]; rec != nil {
		m.svc.MarkSeen(rec, m.probes[name])
	}
}

func (m *Model) attach() tea.Cmd {
	it, ok := m.selItem()
	if !ok || it.busy != "" {
		return nil
	}
	switch {
	case !it.running():
		return m.resume(true)
	case !it.agentUp:
		name := it.name()
		return m.run(name, []core.Step{m.svc.StartAgent(it.rec, "", true)}, func(string) tea.Cmd {
			delete(m.busy, name)
			return m.attachCmd(name)
		}, "")
	}
	return m.attachCmd(it.name())
}

func (m *Model) openReply() tea.Cmd {
	it, ok := m.selItem()
	if !ok {
		return nil
	}
	if it.probe == nil || it.probe.Recap == nil {
		if !agent.ReportsActivity(it.rec.Agent) {
			return m.say(agentLabel(it.rec.Agent)+" doesn't report its replies", cWarn)
		}
		return m.say("No reply saved yet. It appears after the agent's next turn", cWarn)
	}
	m.reply, m.rsc = true, 0
	m.markSeen(it.name())
	return nil
}

// diffView is the overlay with the full diff behind the Changes tab.
type diffView struct {
	name    string
	loading bool
	err     string
	files   []agent.FileDiff
	sc      int
	// jump is a file to scroll to once the diff has loaded.
	jump string
	// starts are where each file begins in the overlay, from the last render.
	starts []int
}

// openDiff loads the selected sandbox's diff into an overlay, scrolled to
// path when it is set.
func (m *Model) openDiff(path string) tea.Cmd {
	it, ok := m.selItem()
	if !ok {
		return nil
	}
	if it.probe == nil || len(it.probe.Files) == 0 {
		return m.say("No changed files to show", cWarn)
	}
	name := it.name()
	run, err := m.svc.Diff(it.rec, m.projectByID(it.rec.ProjectID), it.live, it.running())
	if err != nil {
		return m.say("Resume the sandbox to see its diff", cWarn)
	}
	m.diff = &diffView{name: name, loading: true, jump: path}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := run(ctx)
		return diffMsg{name, raw, err}
	}
}

func (m *Model) onDiff(msg diffMsg) {
	d := m.diff
	if d == nil || d.name != msg.name {
		return
	}
	d.loading = false
	if msg.err != nil {
		d.err = "Could not read the diff: " + msg.err.Error()
		return
	}
	d.files = agent.ParseDiff(msg.raw)
}

// diffFile moves the diff overlay to the next (+1) or previous (-1) file.
// Going back from inside a file returns to its top first.
func (m *Model) diffFile(dir int) {
	d := m.diff
	if len(d.starts) == 0 {
		return
	}
	cur := 0
	for i, s := range d.starts {
		if s <= d.sc {
			cur = i
		}
	}
	next := cur + dir
	if dir < 0 && d.sc > d.starts[cur] {
		next = cur
	}
	d.sc = d.starts[max(0, min(len(d.starts)-1, next))]
}

func (m *Model) shell() tea.Cmd {
	it, ok := m.selItem()
	if !ok || !it.running() {
		return nil
	}
	name := it.name()
	return tea.ExecProcess(m.be.Interactive(name, agent.ShellArgv()...), func(err error) tea.Msg {
		return execDoneMsg{name: name, err: err}
	})
}

func (m *Model) resume(attachAfter bool) tea.Cmd {
	it, ok := m.selItem()
	if !ok || it.running() || it.busy != "" {
		return nil
	}
	name := it.name()
	var then func(string) tea.Cmd
	if attachAfter {
		then = func(string) tea.Cmd { delete(m.busy, name); return m.attachCmd(name) }
	}
	return m.run(name, m.svc.Resume(it.rec), then, "Resumed "+name)
}

func (m *Model) stop() tea.Cmd {
	it, ok := m.selItem()
	if !ok || !it.running() || it.busy != "" {
		return nil
	}
	name := it.name()
	return m.run(name, m.svc.Stop(name), nil, "Stopped "+name+". Its clone and branch are kept.")
}

func (m *Model) askRemove() tea.Cmd {
	it, ok := m.selItem()
	if !ok || it.busy != "" {
		return nil
	}
	name, running := it.name(), it.running()
	body := "This deletes the sandbox and its agent history."
	if it.rec.Clone {
		body = "This deletes the sandbox, its private clone and agent history. Commits that were not pushed are lost."
	}
	if running {
		body += " The sandbox is stopped first."
	}
	m.confirm = &confirmBox{title: "Remove " + name + "?", body: body, yes: "Remove", fn: func() tea.Cmd {
		return m.run(name, m.svc.Remove(name, running), nil, "Removed "+name)
	}}
	return nil
}

func (m *Model) openPR() tea.Cmd {
	it, ok := m.selItem()
	if !ok || !canPR(it) {
		return nil
	}
	return m.sendPrompt(it, core.PRPrompt(m.cur().DefaultBranch), "Asked "+it.name()+" to open a draft PR")
}

func (m *Model) sendPrompt(it item, text, done string) tea.Cmd {
	return m.run(it.name(), m.svc.Send(it.rec, it.running(), it.agentUp, text), nil, done)
}

func canPR(it item) bool {
	return core.CanPR(it.rec, it.probe) && it.busy == ""
}

func canCheckout(it item) bool {
	return core.CanCheckout(it.rec, it.running()) && it.busy == ""
}

// checkoutLocal pulls a clone-mode sandbox's branch into the project's own
// checkout.
func (m *Model) checkoutLocal() tea.Cmd {
	it, ok := m.selItem()
	p := m.cur()
	if !ok || p == nil || !canCheckout(it) {
		return nil
	}
	return m.run(it.name(), m.svc.CheckoutLocal(*p, it.rec), nil, "Checked out "+it.rec.Branch+" in "+p.Name)
}

func (m *Model) toggleLayout() tea.Cmd {
	p := m.cur()
	if p == nil {
		return nil
	}
	if p.Layout == store.LayoutFeature {
		p.Layout = store.LayoutRepo
	} else {
		p.Layout = store.LayoutFeature
	}
	_ = m.st.SetLayout(p.ID, p.Layout)
	m.sel, m.tab, m.dsc = "", tabActivity, 0
	m.fixSel()
	if p.Layout == store.LayoutRepo {
		return m.say("One sandbox for the repo, working in your checkout", cFg)
	}
	return m.say("One sandbox per feature, each on a private clone", cFg)
}

func (m *Model) startRepoSandbox() tea.Cmd {
	p := m.cur()
	if p == nil {
		return nil
	}
	f := newSandboxForm(p)
	f.ws = "direct"
	f.agent = "claude"
	f.repo = true
	return m.create(f)
}

func (m *Model) create(f *newForm) tea.Cmd {
	p := m.cur()
	if p == nil {
		return nil
	}
	req := core.CreateRequest{Agent: f.agent, Source: f.source(), Value: f.value.String(), Task: f.task.String(),
		Clone: f.ws == "clone", Kits: parseKits(f.kits.String()), MCPs: f.pickedMCPs(),
		RememberMCPs: f.mcpLoaded && f.mcpErr == ""}
	if len(core.ModelsFor(f.agent)) > 0 {
		req.Model = f.model
	}
	rec, steps, err := m.svc.Create(p, req, func(name string) bool {
		_, live := m.live[name]
		_, rec := m.recs[name]
		return live || rec
	})
	if err != nil {
		return m.say("Could not save the sandbox: "+err.Error(), cBad)
	}
	m.recs[rec.Name] = rec
	m.modal = nil
	m.selectName(rec.Name)
	m.tab = tabActivity
	return m.run(rec.Name, steps, nil, fmt.Sprintf("Created %s", rec.Name))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// ideCLIs are IDE launchers found on PATH, in the order they're preferred.
var ideCLIs = []string{"cursor", "code", "zed", "windsurf", "goland", "idea", "subl"}

// ideApps are the macOS apps tried with open -a when no launcher is on PATH,
// which is the case until "Install shell command" has been run in the IDE.
var ideApps = []string{"Cursor", "Visual Studio Code", "Zed", "Windsurf", "GoLand", "IntelliJ IDEA", "Sublime Text"}

// editorCmd picks how to open path. gui is true for commands that return
// straight away and don't need the terminal.
func editorCmd(path string) (c *exec.Cmd, gui bool) {
	if ed := os.Getenv("SBX_PROJECTS_EDITOR"); ed != "" {
		return exec.Command("sh", "-c", ed+` "$1"`, "sh", path), true
	}
	for _, bin := range ideCLIs {
		if _, err := exec.LookPath(bin); err == nil {
			return exec.Command(bin, path), true
		}
	}
	if runtime.GOOS == "darwin" {
		for _, app := range ideApps {
			for _, dir := range []string{"/Applications", filepath.Join(os.Getenv("HOME"), "Applications")} {
				if _, err := os.Stat(filepath.Join(dir, app+".app")); err == nil {
					return exec.Command("open", "-a", app, path), true
				}
			}
		}
	}
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	switch {
	case ed != "":
		return exec.Command("sh", "-c", ed+` "$1"`, "sh", path), false
	case runtime.GOOS == "darwin":
		return exec.Command("open", path), true
	default:
		return exec.Command("xdg-open", path), true
	}
}

func (m *Model) openEditor() tea.Cmd {
	p := m.cur()
	if p == nil {
		return nil
	}
	if m.mock {
		return m.say("Would open "+p.Path+" in your editor", cOk)
	}
	c, gui := editorCmd(p.Path)
	if !gui {
		return tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{err: err} })
	}
	return func() tea.Msg {
		if out, err := c.CombinedOutput(); err != nil {
			if msg := strings.TrimSpace(lastLine(string(out))); msg != "" {
				err = errors.New(msg)
			}
			return execDoneMsg{err: err}
		}
		return nil
	}
}

func canOpenIDE(it item) bool {
	return it.rec.Clone && it.running()
}

// openIDE opens a clone-mode sandbox's own clone in Cursor or VS Code over
// Remote-SSH, using the NAME.sbx host that sbx sets up for every sandbox.
// $SBXP_IDE picks the editor CLI; otherwise cursor, then code, from PATH.
func (m *Model) openIDE() tea.Cmd {
	it, ok := m.selItem()
	if !ok || !canOpenIDE(it) {
		return nil
	}
	ws := it.live.Workspace()
	if ws == "" {
		ws = m.projectByID(it.rec.ProjectID).Path
	}
	host := it.name() + ".sbx"
	if m.mock {
		return m.say("Would open "+host+":"+ws+" in your IDE", cOk)
	}
	ide := os.Getenv("SBXP_IDE")
	if ide == "" {
		for _, c := range []string{"cursor", "code"} {
			if _, err := exec.LookPath(c); err == nil {
				ide = c
				break
			}
		}
	}
	if ide == "" {
		return m.say("Install Cursor's or VS Code's shell command, or set SBXP_IDE", cWarn)
	}
	c := exec.Command(ide, "--remote", "ssh-remote+"+host, ws)
	if err := c.Start(); err != nil {
		return m.say(err.Error(), cBad)
	}
	go c.Wait()
	return m.say("Opening "+host+" in "+filepath.Base(ide), cOk)
}

func (m *Model) addProject(path string) tea.Cmd {
	path = expandHome(strings.TrimSpace(path))
	p, err := inspectRepo(path, m.mock)
	if err != nil {
		return m.say(err.Error(), cBad)
	}
	added, err := m.st.AddProject(p)
	if errors.Is(err, store.ErrExists) {
		return m.say(p.Path+" is already a project", cWarn)
	}
	if err != nil {
		return m.say(err.Error(), cBad)
	}
	m.prompt = nil
	_ = m.reload()
	for i, pp := range m.projects {
		if pp.ID == added.ID {
			m.setProject(i)
		}
	}
	return tea.Batch(m.say("Added "+added.Name, cOk), m.listCmd())
}

func (m *Model) askRemoveProject() tea.Cmd {
	p := m.cur()
	if p == nil {
		return nil
	}
	id, name := p.ID, p.Name
	m.confirm = &confirmBox{title: "Forget " + name + "?", body: "Its sandboxes keep running and stay in sbx. You can add the project again later.", yes: "Forget", fn: func() tea.Cmd {
		_ = m.st.RemoveProject(id)
		_ = m.reload()
		m.setProject(m.P)
		return m.say("Forgot "+name, cOk)
	}}
	return nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func inspectRepo(path string, mock bool) (store.Project, error) {
	if path == "" {
		return store.Project{}, errors.New("enter the path to a git repository")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return store.Project{}, err
	}
	if mock {
		return store.Project{Name: filepath.Base(abs), Path: abs, DefaultBranch: "main"}, nil
	}
	root, err := gitOut(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return store.Project{}, fmt.Errorf("%s is not inside a git repository", abs)
	}
	p := store.Project{Name: filepath.Base(root), Path: root, DefaultBranch: "main"}
	if u, err := gitOut(root, "remote", "get-url", "origin"); err == nil {
		p.Remote = prettyRemote(u)
	}
	if h, err := gitOut(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		p.DefaultBranch = strings.TrimPrefix(h, "origin/")
	}
	return p, nil
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func prettyRemote(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "ssh://")
	u = strings.TrimPrefix(u, "git@")
	return strings.Replace(u, ":", "/", 1)
}

// DefaultProjectPath is the git root of the working directory, if any.
func DefaultProjectPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if root, err := gitOut(wd, "rev-parse", "--show-toplevel"); err == nil {
		return root
	}
	return wd
}
