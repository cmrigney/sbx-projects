package ui

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/applog"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

func newTestModel(t *testing.T) (*Model, *sbx.Mock) {
	t.Helper()
	listEvery = time.Millisecond
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := st.AddProject(store.Project{Name: "repo", Path: "/src/repo"})
	if err != nil {
		t.Fatal(err)
	}
	mock := sbx.NewMock([]sbx.MockSeed{
		{Name: "claude-a", Agent: "claude", Workspace: "/src/repo", Branch: "feat/a", Running: true, AgentUp: true, Clone: true, CPUs: 2, MemoryMiB: 4096,
			Events: []map[string]any{{"ts": 1, "ev": "Stop", "msg": "done"}}, Commits: []string{"abc1234\t1\tfeat: a"},
			Files: []string{"30\t0\ta.go", "2\t1\tb.go"}},
		{Name: "claude-b", Agent: "claude", Workspace: "/src/repo", Branch: "feat/b"},
		{Name: "other", Agent: "shell", Workspace: "/elsewhere"},
	})
	_ = st.UpsertSandbox(store.Sandbox{Name: "claude-a", ProjectID: p.ID, Title: "Feature A", Agent: "claude", Clone: true, Source: "branch"})
	m, err := New(Options{Backend: mock, Store: st, Mock: true, HostCPUs: 8, HostMemMiB: 16384})
	if err != nil {
		t.Fatal(err)
	}
	return m, mock
}

// drain runs a command and feeds resulting messages back into the model,
// skipping the recurring ticks so it terminates.
func drain(m *Model, cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 50; i++ {
		msg := cmd()
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				drain(m, c)
			}
			return
		case listTick, spinTick, clearMsg, nil:
			return
		}
		_, cmd = m.Update(msg)
	}
}

func TestListAdoptsSandboxesInsideProjects(t *testing.T) {
	m, _ := newTestModel(t)
	drain(m, m.listCmd())
	if _, ok := m.recs["claude-b"]; !ok {
		t.Fatal("claude-b's workspace is inside the project and should be adopted")
	}
	if len(m.loose) != 1 || m.loose[0].Name != "other" {
		t.Fatalf("loose = %+v, want only 'other'", m.loose)
	}
	if p := m.probes["claude-a"]; p == nil || len(p.Commits) != 1 {
		t.Fatalf("running sandbox should have been probed, got %+v", p)
	}
	if _, probed := m.probeAt["claude-b"]; probed {
		t.Fatal("stopped sandboxes must never be probed: sbx exec would start them")
	}
}

func TestRendersAtManySizes(t *testing.T) {
	m, _ := newTestModel(t)
	drain(m, m.listCmd())
	overlays := []func(){
		func() {},
		func() { m.help = true },
		func() { m.openNew() },
		func() { drain(m, m.openNew()); m.modal.expand(); m.modal.f = fMCP },
		func() { m.openAddProject() },
		func() { _ = m.openKits() },
		func() { _ = m.askRemove() },
		func() {
			m.selectName("claude-a")
			m.probes["claude-a"].Recap = &agent.Recap{When: time.Now(), Text: strings.Repeat("A long reply line that wraps. ", 40)}
			_ = m.openReply()
		},
		func() { m.selectName("claude-a"); drain(m, m.openDiff("b.go")) },
	}
	for _, open := range overlays {
		for _, size := range [][2]int{{20, 8}, {40, 12}, {60, 20}, {90, 30}, {104, 30}, {150, 45}, {240, 70}} {
			m.closeOverlay()
			open()
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			v := m.View()
			if lines := strings.Count(v.Content, "\n") + 1; lines != size[1] {
				t.Fatalf("%dx%d: rendered %d lines", size[0], size[1], lines)
			}
		}
	}
}

func TestCreateRunsTheWholeChain(t *testing.T) {
	m, mock := newTestModel(t)
	drain(m, m.listCmd())
	m.openNew()
	m.modal.value.insert("feat/new-thing")
	m.modal.task.insert("Do the thing")
	drain(m, m.create(m.modal))

	rec := m.recs["claude-new-thing"]
	if rec == nil || rec.Branch != "feat/new-thing" || !rec.Clone || rec.Title != "Do the thing" {
		t.Fatalf("record = %+v", rec)
	}
	if m.busy["claude-new-thing"] != "" {
		t.Fatalf("still busy: %q", m.busy["claude-new-thing"])
	}
	out, err := mock.Exec(context.Background(), "claude-new-thing", "@@agent")
	if err != nil || !strings.Contains(out, "Do the thing") {
		t.Fatalf("the first instruction should reach the agent: %v\n%s", err, out)
	}
}

func TestListOrderIsStable(t *testing.T) {
	m, _ := newTestModel(t)
	p := m.cur()
	created := time.Unix(1000, 0)
	for _, name := range []string{"p-one", "p-two", "p-three", "p-four", "p-five"} {
		r := store.Sandbox{Name: name, ProjectID: p.ID, Agent: "claude", CreatedAt: created}
		m.recs[name] = &r
		m.live[name] = sbx.Sandbox{Name: name, Status: "stopped"}
	}
	names := func() string {
		var out []string
		for _, it := range m.ordered() {
			out = append(out, it.name())
		}
		return strings.Join(out, ",")
	}
	want := names()
	m.selectName("p-three")
	for i := 0; i < 50; i++ {
		// Activity on one sandbox must not move the others.
		now := time.Now()
		s := m.live["p-two"]
		s.LastUsedAt = &now
		m.live["p-two"] = s
		if got := names(); got != want {
			t.Fatalf("order changed between renders:\n got %s\nwant %s", got, want)
		}
	}
	if it, _ := m.selItem(); it.name() != "p-three" {
		t.Fatalf("selection moved to %s", it.name())
	}
}

func TestNewSandboxesStartWithTheProjectsKits(t *testing.T) {
	m, mock := newTestModel(t)
	drain(m, m.listCmd())
	_ = m.openKits()
	m.prompt.field.insert("./kits/go, ghcr.io/acme/node:1")
	drain(m, m.submitPrompt())
	if got := strings.Join(m.cur().Kits, " "); got != "./kits/go ghcr.io/acme/node:1" {
		t.Fatalf("project kits = %q", got)
	}

	m.openNew()
	m.modal.value.insert("feat/kits")
	if c := m.modal.command(); !strings.Contains(c, "--kit ./kits/go --kit ghcr.io/acme/node:1 claude") {
		t.Fatalf("command preview = %q", c)
	}
	// Kits can be changed for one sandbox without touching the defaults.
	m.modal.kits.insert(" ./kits/extra")
	drain(m, m.create(m.modal))

	want := "./kits/go ghcr.io/acme/node:1 ./kits/extra"
	if got := strings.Join(mock.Kits("claude-kits"), " "); got != want {
		t.Fatalf("sbx got kits %q, want %q", got, want)
	}
	if got := strings.Join(m.recs["claude-kits"].Kits, " "); got != want {
		t.Fatalf("record kits = %q", got)
	}
	if got := len(m.cur().Kits); got != 2 {
		t.Fatalf("project defaults changed: %q", m.cur().Kits)
	}
}

func TestNewSandboxRemembersModelsByAgent(t *testing.T) {
	m, _ := newTestModel(t)
	drain(m, m.listCmd())
	m.openNew()
	if m.modal.model != "" {
		t.Fatalf("first form model = %q", m.modal.model)
	}
	m.modal.value.insert("feat/model")
	m.modal.model = "opus"
	drain(m, m.create(m.modal))
	if got := m.recs["claude-model"].ClaudeModel; got != "opus" {
		t.Fatalf("record model = %q", got)
	}
	m.openNew()
	if m.modal.model != "opus" {
		t.Fatalf("form should start with the last pick, got %q", m.modal.model)
	}
	// Codex has its own remembered model and does not change Claude's pick.
	m.modal.setAgent("codex")
	m.modal.expand()
	if m.modal.f != fModel {
		t.Fatal("codex should have a model row")
	}
	m.modal.model = "gpt-6.1-sol"
	m.modal.value.insert("feat/codex")
	drain(m, m.create(m.modal))
	if rec := m.recs["codex-codex"]; rec.ClaudeModel != "" || rec.CodexModel != "gpt-6.1-sol" {
		t.Fatalf("codex record models: %+v", rec)
	}
	if m.cur().ClaudeModel != "opus" || m.cur().CodexModel != "gpt-6.1-sol" {
		t.Fatalf("project models: %+v", m.cur())
	}
	m.openNew()
	m.modal.setAgent("codex")
	if m.modal.model != "gpt-6.1-sol" {
		t.Fatalf("codex form should start with the last pick, got %q", m.modal.model)
	}
}

func TestNewSandboxFoldsTheExtraRows(t *testing.T) {
	m, _ := newTestModel(t)
	drain(m, m.listCmd())
	_ = m.openKits()
	m.prompt.field.insert("./kits/go ./kits/node")
	drain(m, m.submitPrompt())
	drain(m, m.openNew())
	f := m.modal
	for range fieldCount {
		f.move(1)
		if f.f == fModel || f.f == fKits || f.f == fMCP {
			t.Fatalf("tab reached hidden row %d", f.f)
		}
	}
	if got := segText(f.moreSummary()); got != "Default model · 2 kits · no MCP servers" {
		t.Fatalf("summary = %q", got)
	}
	f.f = fMore
	m.modalKey(tea.KeyPressMsg{Code: tea.KeySpace})
	if !f.more || f.f != fModel {
		t.Fatalf("space should show the rows and focus the model, got more=%v f=%d", f.more, f.f)
	}
	f.move(-1)
	if f.f == fMore {
		t.Fatal("the summary row should go once the rows show")
	}
	// Without Claude there is no model to show first.
	drain(m, m.openNew())
	m.modal.setAgent("codex")
	m.modal.expand()
	if m.modal.f != fModel {
		t.Fatalf("focus = %d, want model", m.modal.f)
	}
}

func segText(segs []Seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.t)
	}
	return b.String()
}

func TestNewSandboxPicksMCPServers(t *testing.T) {
	m, mock := newTestModel(t)
	drain(m, m.listCmd())
	drain(m, m.openNew())
	if len(m.modal.mcps) != 3 || len(m.modal.pickedMCPs()) != 0 {
		t.Fatalf("listed %+v, picked %q", m.modal.mcps, m.modal.pickedMCPs())
	}
	m.modal.value.insert("feat/mcp")
	m.modal.expand()
	m.modal.f = fKits
	m.modal.move(1)
	if m.modal.f != fMCP {
		t.Fatalf("focus = %d, want the MCP row", m.modal.f)
	}
	press := func(code rune) { m.modalKey(tea.KeyPressMsg{Code: code}) }
	press(tea.KeySpace) // notion
	press(tea.KeyRight)
	press(tea.KeyRight)
	press(tea.KeySpace) // linear
	if c := m.modal.command(); !strings.Contains(c, "--static-mcp notion,linear claude") {
		t.Fatalf("command preview = %q", c)
	}
	drain(m, m.create(m.modal))
	if got := strings.Join(mock.MCPs("claude-mcp"), ","); got != "notion,linear" {
		t.Fatalf("sbx got %q", got)
	}
	if got := strings.Join(m.recs["claude-mcp"].MCPs, ","); got != "notion,linear" {
		t.Fatalf("record mcps = %q", got)
	}

	// The next form starts with that pick, minus servers that are gone.
	mock.SetMCPServers([]sbx.MCPServer{{Name: "notion", Status: "ready"}})
	drain(m, m.openNew())
	if got := strings.Join(m.modal.pickedMCPs(), ","); got != "notion" {
		t.Fatalf("picked = %q", got)
	}

	// With nothing registered the row is skipped.
	mock.SetMCPServers(nil)
	drain(m, m.openNew())
	m.modal.expand()
	m.modal.f = fKits
	m.modal.move(1)
	if m.modal.f == fMCP {
		t.Fatal("focused an empty MCP row")
	}
}

func TestRemovingARunningSandboxStopsItFirst(t *testing.T) {
	m, mock := newTestModel(t)
	drain(m, m.listCmd())
	m.selectName("claude-a")
	_ = m.askRemove()
	if m.confirm == nil {
		t.Fatal("a running sandbox should still ask to be removed")
	}
	drain(m, m.confirmYes())
	sbxs, _ := mock.List(context.Background())
	for _, s := range sbxs {
		if s.Name == "claude-a" {
			t.Fatalf("claude-a was not removed: %+v", s)
		}
	}
	if _, ok := m.recs["claude-a"]; ok {
		t.Fatal("claude-a's record should be gone")
	}
}

func plainLines(ls []Line) []string {
	var out []string
	for _, l := range ls {
		var b strings.Builder
		for _, sg := range l.segs {
			b.WriteString(sg.t)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func TestRenderMarkdown(t *testing.T) {
	md := "## What changed\n" +
		"- **Bold** and `a_b` in snake_case\n" +
		"  - nested [the PR](https://x/pr/1)\n" +
		"- [x] done\n" +
		"12. twelfth\n" +
		"> quoted\n" +
		"---\n" +
		"```go\n" +
		"x :=  1 // **kept**\n" +
		"```\n" +
		"\n" +
		"| File | Change | Lines |\n" +
		"|:-----|--------|------:|\n" +
		"| `a.go` | added a thing | 12 |\n" +
		"| b.go | x \\| y | 3 |\n"
	got := strings.Join(plainLines(renderMarkdown(md, 40)), "\n")
	want := strings.Join([]string{
		"What changed",
		"• Bold and a_b in snake_case",
		"  • nested the PR (https://x/pr/1)",
		"• ☑ done",
		"12. twelfth",
		"│ quoted",
		strings.Repeat("─", 40),
		"  x :=  1 // **kept**",
		"",
		"File │ Change        │ Lines",
		"─────┼───────────────┼──────",
		"a.go │ added a thing │    12",
		"b.go │ x | y         │     3",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\n\nwant\n%s", got, want)
	}
}

func TestRenderMarkdownNarrowTables(t *testing.T) {
	md := "| Step | What happened |\n|---|---|\n| Build | The build passed after fixing the import cycle in the ui package |\n"
	got := plainLines(renderMarkdown(md, 30))
	want := []string{
		"Step  │ What happened",
		"──────┼───────────────────────",
		"Build │ The build passed after",
		"      │ fixing the import",
		"      │ cycle in the ui",
		"      │ package",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("wrapped table:\n%s", strings.Join(got, "\n"))
	}
	for _, l := range renderMarkdown(md, 30) {
		if segsWidth(l.segs) > 30 {
			t.Fatalf("line wider than 30: %q", plainLines([]Line{l}))
		}
	}
	wide := "| a | b | c | d | e |\n|-|-|-|-|-|\n| 1 | 2 | 3 | 4 | 5 |\n"
	if got := plainLines(renderMarkdown(wide, 20)); strings.Join(got, "\n") != "a: 1\nb: 2\nc: 3\nd: 4\ne: 5" {
		t.Fatalf("too narrow for columns, want one line per cell:\n%s", strings.Join(got, "\n"))
	}
}

func TestReplyOpensInAnOverlay(t *testing.T) {
	m, _ := newTestModel(t)
	drain(m, m.listCmd())
	m.selectName("claude-a")
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 30})
	plain := func() string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(m.View().Content, "") }

	m.probes["claude-a"].Recap = nil
	m.onKey(tea.KeyPressMsg{Code: 't', Text: "t"})
	if m.reply || !strings.Contains(m.msg, "No reply saved yet") {
		t.Fatalf("with no reply saved: open=%v msg=%q", m.reply, m.msg)
	}

	text := "Shipped it.\n\n## Notes\n- tests pass"
	for i := 0; i < 40; i++ {
		text += fmt.Sprintf("\nline %d", i)
	}
	m.probes["claude-a"].Recap = &agent.Recap{When: time.Now(), Text: text}
	if v := plain(); strings.Contains(v, "Shipped it.") || !strings.Contains(v, "t New reply") {
		t.Fatal("the Activity tab should only offer the reply, not show it")
	}
	m.onKey(tea.KeyPressMsg{Code: 't', Text: "t"})
	v := plain()
	for _, want := range []string{"Last reply from claude-a", "Shipped it.", "Notes", "• tests pass", "j/k scroll ▾"} {
		if !strings.Contains(v, want) {
			t.Errorf("overlay missing %q", want)
		}
	}
	for i := 0; i < 100; i++ {
		m.onKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if v := plain(); !strings.Contains(v, "line 39") || !strings.Contains(v, "▴ j/k scroll") {
		t.Fatal("scrolling down should reach the end of the reply")
	}
	m.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.reply {
		t.Fatal("esc should close the reply")
	}
}

func TestUnreadReplies(t *testing.T) {
	m, _ := newTestModel(t)
	drain(m, m.listCmd())
	m.selectName("claude-a")
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 30})
	plain := func() string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(m.View().Content, "") }
	unread := func() bool { it, _ := m.selItem(); return it.unread }

	reply := func() { m.probes["claude-a"].Recap = &agent.Recap{When: time.Now().Add(time.Second), Text: "done"} }
	reply()
	if !unread() || !strings.Contains(plain(), "t New reply") {
		t.Fatal("a reply the user hasn't read should be marked")
	}
	m.onKey(tea.KeyPressMsg{Code: 't', Text: "t"})
	m.closeOverlay()
	if unread() || !strings.Contains(plain(), "t Last reply") {
		t.Fatal("reading the reply should clear the mark")
	}
	if sbs, _ := m.st.Sandboxes(); sbs[0].SeenAt.IsZero() {
		t.Fatal("the seen time should be saved")
	}

	m.recs["claude-a"].SeenAt = time.Time{}
	reply()
	m.Update(execDoneMsg{name: "claude-a", attached: true})
	if unread() {
		t.Fatal("attaching should clear the mark")
	}
	m.recs["claude-a"].SeenAt = time.Time{}
	m.Update(execDoneMsg{name: "claude-a"})
	if !unread() {
		t.Fatal("a shell session is not the agent's, so the mark should stay")
	}
}

func TestDiffOpensInAnOverlay(t *testing.T) {
	m, _ := newTestModel(t)
	drain(m, m.listCmd())
	m.selectName("claude-a")
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 30})
	plain := func() string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(m.View().Content, "") }

	m.onKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.diff != nil || strings.Contains(plain(), "d Diff") {
		t.Fatal("d should only work in the Changes tab")
	}
	m.tab = tabChanges

	files := m.probes["claude-a"].Files
	m.probes["claude-a"].Files = nil
	m.onKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.diff != nil || !strings.Contains(m.msg, "No changed files") {
		t.Fatalf("with nothing changed: open=%v msg=%q", m.diff != nil, m.msg)
	}
	m.probes["claude-a"].Files = files
	if v := plain(); !strings.Contains(v, "d Show the diff") || !strings.Contains(v, "d Diff") {
		t.Fatal("the Changes tab should offer the diff")
	}
	drain(m, m.onKey(tea.KeyPressMsg{Code: 'd', Text: "d"}))
	v := plain()
	for _, want := range []string{"Diff of claude-a", "a.go", "new file", "+30 −0", "+    new line 1", "j/k scroll ▾", "n/N Next or previous file"} {
		if !strings.Contains(v, want) {
			t.Errorf("overlay missing %q", want)
		}
	}
	m.onKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if v := plain(); !strings.Contains(v, "-    old line 1") {
		t.Fatal("n should jump to the next file")
	}
	m.onKey(tea.KeyPressMsg{Code: 'N', Text: "N"})
	if m.diff.sc != 0 {
		t.Fatalf("N should go back to the first file, sc=%d", m.diff.sc)
	}
	m.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.diff != nil {
		t.Fatal("esc should close the diff")
	}

	drain(m, m.openDiff("b.go"))
	if v := plain(); m.diff.sc == 0 || !strings.Contains(v, "old line 1") {
		t.Fatal("opening at a file should scroll to it")
	}
}

func TestLogOpensInAnOverlay(t *testing.T) {
	m, _ := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 30})
	plain := func() string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(m.View().Content, "") }

	m.onKey(tea.KeyPressMsg{Code: 'L', Text: "L"})
	if !strings.Contains(plain(), "Logging is off") {
		t.Fatal("with no log, the view should say logging is off")
	}
	m.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	at := time.Date(2026, 10, 23, 10, 55, 33, 0, time.Local)
	entries := []applog.Entry{{Time: at, Level: slog.LevelError, Msg: "sbx failed", Attrs: []applog.Attr{
		{Key: "args", Value: "[exec -i claude-a bash -s]"}, {Key: "stdout", Value: "@@branch\nfeat/a\n@@diff"}}}}
	for i := range 40 {
		entries = append(entries, applog.Entry{Time: at, Level: slog.LevelDebug, Msg: "sbx ran", Attrs: []applog.Attr{{Key: "i", Value: fmt.Sprint(i)}}})
	}
	m.logPath, m.logs = "/tmp/sbx-projects/run.log", func() []applog.Entry { return entries }

	m.onKey(tea.KeyPressMsg{Code: 'L', Text: "L"})
	if v := plain(); !strings.Contains(v, "i  39") || strings.Contains(v, "sbx failed") {
		t.Fatal("the log should open on its newest entries")
	}
	entries = append(entries, applog.Entry{Time: at, Level: slog.LevelInfo, Msg: "newest"})
	if !strings.Contains(plain(), "newest") {
		t.Fatal("the log should follow new entries")
	}
	m.onKey(tea.KeyPressMsg{Code: 'g', Text: "g"})
	entries = append(entries, applog.Entry{Time: at, Level: slog.LevelInfo, Msg: "later"})
	v := plain()
	for _, want := range []string{"/tmp/sbx-projects/run.log", "10:55:33  ERROR  sbx failed", "args  [exec -i claude-a bash -s]", "      @@branch", "      feat/a"} {
		if !strings.Contains(v, want) {
			t.Errorf("log missing %q", want)
		}
	}
	if strings.Contains(v, "later") {
		t.Error("scrolled up, the log should stay put")
	}

	m.onKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if v := plain(); !strings.Contains(v, "sbx failed") || strings.Contains(v, "sbx ran") || !strings.Contains(v, "warnings and errors") {
		t.Fatal("e should show only warnings and errors")
	}
	m.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.log != nil {
		t.Fatal("esc should close the log")
	}
}

func TestChop(t *testing.T) {
	if got := chop("abcdefg", 3); !slices.Equal(got, []string{"abc", "def", "g"}) {
		t.Fatalf("chop = %q", got)
	}
	if got := chop("", 3); !slices.Equal(got, []string{""}) {
		t.Fatalf("chop empty = %q", got)
	}
}

func TestOneOffListDoesNotStartAnotherPoll(t *testing.T) {
	m, _ := newTestModel(t)
	_, cmd := m.Update(m.listCmd()())
	if hasListTick(cmd) {
		t.Fatal("a one-off refresh scheduled a poll; each would add a polling chain")
	}
	_, cmd = m.Update(m.pollCmd()())
	if !hasListTick(cmd) {
		t.Fatal("the poll should schedule the next one")
	}
}

// hasListTick reports whether cmd, run through any batches, schedules a
// listTick. Ticks wait listEvery, which tests set to a millisecond.
func hasListTick(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if hasListTick(c) {
				return true
			}
		}
	case listTick:
		return true
	}
	return false
}
