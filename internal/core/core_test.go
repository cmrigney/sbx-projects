package core

import (
	"context"
	"strings"
	"testing"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

func newTestService(t *testing.T, seeds []sbx.MockSeed) (*Service, *sbx.Mock, store.Project) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := st.AddProject(store.Project{Name: "repo", Path: "/src/repo"})
	if err != nil {
		t.Fatal(err)
	}
	mock := sbx.NewMock(seeds)
	return &Service{Backend: mock, Store: st, Demo: true}, mock, p
}

func TestSlugAndDigits(t *testing.T) {
	for in, want := range map[string]string{
		"feat/Ports UI hints":                 "ports-ui-hints",
		"main":                                "main",
		"///":                                 "work",
		"refactor/" + strings.Repeat("x", 60): strings.Repeat("x", 40),
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"#4127": "4127", "https://github.com/cmrigney/skill-share/pull/4127": "4127", "nope": "0"} {
		if got := Digits(in); got != want {
			t.Errorf("Digits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSandboxName(t *testing.T) {
	for _, c := range []struct{ source, value, want string }{
		{"branch", "feat/Rate limit", "claude-rate-limit"},
		{"branch", "  ", "claude-repo"},
		{"pr", "#4127", "claude-pr-4127"},
		{"repo", "ignored", "claude-repo"},
	} {
		if got := SandboxName("claude", "repo", c.source, c.value); got != c.want {
			t.Errorf("SandboxName(%q, %q) = %q, want %q", c.source, c.value, got, c.want)
		}
	}
	taken := func(n string) bool { return n == "claude-x" || n == "claude-x-2" }
	if got := uniqueName("claude-x", taken); got != "claude-x-3" {
		t.Errorf("a taken name should gain the next free number: %q", got)
	}
	if got := uniqueName("claude-y", taken); got != "claude-y" {
		t.Errorf("a free name should be kept: %q", got)
	}
}

func TestModels(t *testing.T) {
	if ModelLabel("codex", "gpt-6-luna") != "GPT-6 Luna" || ModelLabel("claude", "") != "Default" || ModelLabel("shell", "x") != "x" {
		t.Fatal("labels")
	}
	rec := &store.Sandbox{Agent: "codex", ClaudeModel: "opus", CodexModel: "gpt-6-luna"}
	if SandboxModel(rec) != "gpt-6-luna" {
		t.Fatalf("codex should start with its own model, got %q", SandboxModel(rec))
	}
	rec.Agent = "shell"
	if SandboxModel(rec) != "" {
		t.Fatal("agents without models take no --model")
	}
}

func TestCreateRunsTheWholeChain(t *testing.T) {
	s, mock, p := newTestService(t, nil)
	rec, steps, err := s.Create(&p, CreateRequest{Agent: "codex", Source: "pr", Value: "#12", Clone: true,
		Model: "gpt-6-luna", MCPs: []string{"notion"}, RememberMCPs: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "codex-pr-12" || rec.Title != "Review PR #12" || rec.CodexModel != "gpt-6-luna" {
		t.Fatalf("record = %+v", rec)
	}
	if len(steps) != 3 {
		t.Fatalf("want create, checkout and start; got %d steps", len(steps))
	}
	if err := Run(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if rec.Branch != "pr/12" {
		t.Fatalf("the checked-out branch should be applied, got %q", rec.Branch)
	}
	out, _ := mock.Exec(context.Background(), rec.Name, "@@agent")
	if !strings.Contains(out, "Do not comment on the PR") {
		t.Fatalf("a PR review with no task should get the review prompt:\n%s", out)
	}
	ps, _ := s.Store.Projects()
	if ps[0].CodexModel != "gpt-6-luna" || strings.Join(ps[0].MCPs, ",") != "notion" || p.CodexModel != "gpt-6-luna" {
		t.Fatalf("the project should remember the picks: %+v", ps[0])
	}
	recs, _ := s.Store.Sandboxes()
	if len(recs) != 1 || recs[0].Branch != "pr/12" {
		t.Fatalf("stored = %+v", recs)
	}
}

func TestCreateNumbersATakenName(t *testing.T) {
	s, _, p := newTestService(t, nil)
	first, _, err := s.Create(&p, CreateRequest{Agent: "claude", Source: "branch", Task: "first"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	taken := func(n string) bool { return n == "claude-repo-2" }
	second, _, err := s.Create(&p, CreateRequest{Agent: "claude", Source: "branch", Task: "second"}, taken)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "claude-repo" || second.Name != "claude-repo-3" {
		t.Fatalf("names = %q, %q", first.Name, second.Name)
	}
	recs, _ := s.Store.Sandboxes()
	titles := map[string]string{}
	for _, r := range recs {
		titles[r.Name] = r.Title
	}
	if len(recs) != 2 || titles["claude-repo"] != "first" || titles["claude-repo-3"] != "second" {
		t.Fatalf("the first sandbox should keep its title: %+v", titles)
	}
}

func TestCreateLeavesMCPPickAloneUnlessAsked(t *testing.T) {
	s, _, p := newTestService(t, nil)
	_ = s.Store.SetMCPs(p.ID, []string{"notion"})
	p.MCPs = []string{"notion"}
	if _, _, err := s.Create(&p, CreateRequest{Agent: "claude", Source: "branch", Value: "x", Clone: true}, nil); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Store.Projects()
	if strings.Join(ps[0].MCPs, ",") != "notion" {
		t.Fatalf("MCP pick changed: %v", ps[0].MCPs)
	}
}

func TestCreateCloneLeavesRepoLayout(t *testing.T) {
	s, _, p := newTestService(t, nil)
	p.Layout = store.LayoutRepo
	_, steps, err := s.Create(&p, CreateRequest{Agent: "claude", Source: "branch", Value: "x", Clone: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Layout != store.LayoutFeature {
		t.Fatal("a private clone should switch the project to one sandbox per feature")
	}
	if len(steps) != 3 {
		t.Fatalf("got %d steps", len(steps))
	}
	_, steps, _ = s.Create(&p, CreateRequest{Agent: "claude", Source: "repo"}, nil)
	if len(steps) != 2 {
		t.Fatalf("a direct sandbox has no branch to check out; got %d steps", len(steps))
	}
}

func TestSendStartsWhatIsNeeded(t *testing.T) {
	s, _, _ := newTestService(t, nil)
	rec := &store.Sandbox{Name: "n", Agent: "claude"}
	for _, c := range []struct {
		running, up bool
		verbs       string
	}{
		{false, false, "Starting,Starting the agent in"},
		{true, false, "Starting the agent in"},
		{true, true, "Messaging"},
	} {
		var verbs []string
		for _, st := range s.Send(rec, c.running, c.up, "hi") {
			verbs = append(verbs, st.Verb)
		}
		if got := strings.Join(verbs, ","); got != c.verbs {
			t.Errorf("running=%v up=%v: %s, want %s", c.running, c.up, got, c.verbs)
		}
	}
}

func TestReconcile(t *testing.T) {
	s, mock, p := newTestService(t, []sbx.MockSeed{
		{Name: "inside", Agent: "claude", Workspace: "/src/repo/sub"},
		{Name: "other", Agent: "shell", Workspace: "/elsewhere"},
	})
	_ = s.Store.UpsertSandbox(store.Sandbox{Name: "gone", ProjectID: p.ID})
	_ = s.Store.UpsertSandbox(store.Sandbox{Name: "busy", ProjectID: p.ID})
	recs := map[string]*store.Sandbox{"gone": {Name: "gone"}, "busy": {Name: "busy"}}
	live, _ := mock.List(context.Background())
	r := s.Reconcile([]store.Project{p}, recs, live, func(n string) bool { return n == "busy" })
	if strings.Join(r.Removed, ",") != "gone" || r.Adopted != 1 || len(r.Loose) != 1 || r.Loose[0].Name != "other" {
		t.Fatalf("reconciled = %+v", r)
	}
	stored, _ := s.Store.Sandboxes()
	names := map[string]string{}
	for _, rec := range stored {
		names[rec.Name] = rec.Source
	}
	if _, ok := names["gone"]; ok || names["inside"] != "adopted" {
		t.Fatalf("stored = %v", names)
	}
	if _, ok := names["busy"]; !ok {
		t.Fatalf("stored = %v", names)
	}
}

func TestSaveProbe(t *testing.T) {
	s, _, p := newTestService(t, nil)
	rec := &store.Sandbox{Name: "a", ProjectID: p.ID, Source: "adopted"}
	_ = s.Store.UpsertSandbox(*rec)
	s.SaveProbe(rec, "raw", agent.Probe{Branch: "feat/x", Clone: true})
	if rec.Branch != "feat/x" || !rec.Clone || rec.LastProbe != "raw" {
		t.Fatalf("rec = %+v", rec)
	}
	stored, _ := s.Store.Sandboxes()
	if stored[0].Branch != "feat/x" || !stored[0].Clone || stored[0].LastProbe != "raw" {
		t.Fatalf("stored = %+v", stored[0])
	}
}

func TestDiffNeverStartsAStoppedClone(t *testing.T) {
	s, _, p := newTestService(t, nil)
	if _, err := s.Diff(&store.Sandbox{Name: "a", Clone: true}, p, sbx.Sandbox{}, false); err != ErrNeedsStart {
		t.Fatalf("err = %v", err)
	}
	if run, err := s.Diff(&store.Sandbox{Name: "a", Clone: true}, p, sbx.Sandbox{}, true); err != nil || run == nil {
		t.Fatalf("a running sandbox's diff should be readable: %v", err)
	}
}
