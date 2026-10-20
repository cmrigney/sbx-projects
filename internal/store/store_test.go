package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectsAndSandboxes(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.AddProject(Project{Name: "repo", Path: "/src/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Layout != LayoutFeature || p.DefaultBranch != "main" {
		t.Fatalf("defaults not applied: %+v", p)
	}
	if _, err := st.AddProject(Project{Name: "again", Path: "/src/repo"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate path: err = %v, want ErrExists", err)
	}

	sb := Sandbox{Name: "claude-x", ProjectID: p.ID, Title: "X", Branch: "feat/x", Agent: "claude", Clone: true, Source: "branch"}
	if err := st.UpsertSandbox(sb); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveProbe("claude-x", "@@agent\nup\n", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	sb.Title = "Renamed"
	if err := st.UpsertSandbox(sb); err != nil {
		t.Fatal(err)
	}
	got, err := st.Sandboxes()
	if err != nil || len(got) != 1 {
		t.Fatalf("sandboxes = %+v, %v", got, err)
	}
	if got[0].Title != "Renamed" || !got[0].Clone || got[0].LastProbe == "" {
		t.Fatalf("upsert must update fields and keep the cached probe: %+v", got[0])
	}

	if err := st.SetLayout(p.ID, LayoutRepo); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClaudeModel(p.ID, "opus"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCodexModel(p.ID, "gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	ps, _ := st.Projects()
	if ps[0].Layout != LayoutRepo || ps[0].ClaudeModel != "opus" || ps[0].CodexModel != "gpt-6.1-sol" {
		t.Fatalf("project settings not saved: %+v", ps[0])
	}

	if err := st.RemoveProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Sandboxes(); len(got) != 0 {
		t.Fatalf("removing a project must forget its sandboxes, got %+v", got)
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(Project{Name: "repo", Path: "/src/repo"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if ps, _ := st.Projects(); len(ps) != 1 {
		t.Fatalf("projects after reopen = %+v", ps)
	}
}

func TestKits(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.AddProject(Project{Name: "repo", Path: "/src/repo", Kits: []string{"./kits/go"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetKits(p.ID, []string{"./kits/go", "ghcr.io/acme/node:1"}); err != nil {
		t.Fatal(err)
	}
	ps, _ := st.Projects()
	if got := strings.Join(ps[0].Kits, " "); got != "./kits/go ghcr.io/acme/node:1" {
		t.Fatalf("project kits = %q", got)
	}
	if err := st.SetKits(p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if ps, _ := st.Projects(); ps[0].Kits != nil {
		t.Fatalf("cleared kits = %q", ps[0].Kits)
	}
	if err := st.UpsertSandbox(Sandbox{Name: "s", ProjectID: p.ID, Kits: []string{"./kits/go"}}); err != nil {
		t.Fatal(err)
	}
	if sbs, _ := st.Sandboxes(); len(sbs[0].Kits) != 1 || sbs[0].Kits[0] != "./kits/go" {
		t.Fatalf("sandbox kits = %q", sbs[0].Kits)
	}
}

func TestMCPs(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.AddProject(Project{Name: "repo", Path: "/src/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMCPs(p.ID, []string{"notion", "github"}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := st.Projects(); strings.Join(ps[0].MCPs, " ") != "notion github" {
		t.Fatalf("project mcps = %q", ps[0].MCPs)
	}
	if err := st.UpsertSandbox(Sandbox{Name: "s", ProjectID: p.ID, MCPs: []string{"notion"}}); err != nil {
		t.Fatal(err)
	}
	if sbs, _ := st.Sandboxes(); len(sbs[0].MCPs) != 1 || sbs[0].MCPs[0] != "notion" {
		t.Fatalf("sandbox mcps = %q", sbs[0].MCPs)
	}
}

func TestOpenAddsColumnsToOldDatabases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// The schema before kits existed.
	_, err = db.Exec(`
CREATE TABLE projects (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, path TEXT NOT NULL UNIQUE,
	remote TEXT NOT NULL DEFAULT '', default_branch TEXT NOT NULL DEFAULT 'main', layout TEXT NOT NULL DEFAULT 'feature', created_at INTEGER NOT NULL);
CREATE TABLE sandboxes (name TEXT PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	title TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '', clone INTEGER NOT NULL DEFAULT 0,
	source TEXT NOT NULL DEFAULT 'adopted', created_at INTEGER NOT NULL, last_probe TEXT NOT NULL DEFAULT '', probed_at INTEGER NOT NULL DEFAULT 0);
INSERT INTO projects (name, path, created_at) VALUES ('repo', '/src/repo', 1);
INSERT INTO sandboxes (name, project_id, created_at, probed_at) VALUES ('s', 1, 1, 50);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if ps, err := st.Projects(); err != nil || len(ps) != 1 || ps[0].Kits != nil || ps[0].ClaudeModel != "" {
		t.Fatalf("projects = %+v, %v", ps, err)
	}
	if sbs, err := st.Sandboxes(); err != nil || len(sbs) != 1 || sbs[0].SeenAt.Unix() != 50 {
		t.Fatalf("sandboxes = %+v, %v; replies already probed should count as seen", sbs, err)
	}
	if err := st.SetKits(1, []string{"./k"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClaudeModel(1, "opus"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCodexModel(1, "gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMCPs(1, []string{"notion"}); err != nil {
		t.Fatal(err)
	}
}
