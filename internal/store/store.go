// Package store persists projects and the sandboxes attached to them.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Layout string

const (
	// LayoutFeature gives each feature its own sandbox on a private clone.
	LayoutFeature Layout = "feature"
	// LayoutRepo shares one sandbox that edits the host working tree.
	LayoutRepo Layout = "repo"
)

type Project struct {
	ID            int64
	Name          string
	Path          string
	Remote        string
	DefaultBranch string
	Layout        Layout
	// Kits are the kit references every new sandbox in the project starts
	// with, passed to sbx run as --kit.
	Kits []string
	// ClaudeModel is the model last picked for a Claude sandbox in the
	// project, which the New sandbox form starts with.
	ClaudeModel string
	// CodexModel is the model last picked for a Codex sandbox in the project.
	CodexModel string
	// MCPs are the MCP servers last picked for a sandbox in the project,
	// which the New sandbox form starts with.
	MCPs      []string
	CreatedAt time.Time
}

type Sandbox struct {
	Name      string
	ProjectID int64
	Title     string
	Branch    string
	Agent     string
	Clone     bool
	Kits      []string
	// ClaudeModel is passed to Claude Code as --model; empty keeps its default.
	ClaudeModel string
	// CodexModel is passed to Codex as --model; empty keeps its default.
	CodexModel string
	// MCPs are the MCP servers passed to sbx run as --static-mcp.
	MCPs []string
	// Source is how the sandbox started: "branch", "pr", "repo" or "adopted".
	Source    string
	CreatedAt time.Time
	// LastProbe is the most recent raw probe output, kept so stopped
	// sandboxes still show their last known activity and changes.
	LastProbe string
	ProbedAt  time.Time
	// SeenAt is when the user last read the agent's reply, by opening it
	// or attaching. A reply newer than this is unread.
	SeenAt time.Time
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS projects (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT NOT NULL,
	path           TEXT NOT NULL UNIQUE,
	remote         TEXT NOT NULL DEFAULT '',
	default_branch TEXT NOT NULL DEFAULT 'main',
	layout         TEXT NOT NULL DEFAULT 'feature',
	kits           TEXT NOT NULL DEFAULT '',
	claude_model   TEXT NOT NULL DEFAULT '',
	codex_model    TEXT NOT NULL DEFAULT '',
	mcps           TEXT NOT NULL DEFAULT '',
	created_at     INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sandboxes (
	name       TEXT PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	title      TEXT NOT NULL DEFAULT '',
	branch     TEXT NOT NULL DEFAULT '',
	agent      TEXT NOT NULL DEFAULT '',
	clone      INTEGER NOT NULL DEFAULT 0,
	kits       TEXT NOT NULL DEFAULT '',
	claude_model TEXT NOT NULL DEFAULT '',
	codex_model  TEXT NOT NULL DEFAULT '',
	mcps       TEXT NOT NULL DEFAULT '',
	source     TEXT NOT NULL DEFAULT 'adopted',
	created_at INTEGER NOT NULL,
	last_probe TEXT NOT NULL DEFAULT '',
	probed_at  INTEGER NOT NULL DEFAULT 0,
	seen_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS sandboxes_project ON sandboxes(project_id);
`

// columns were added after the first release. CREATE TABLE IF NOT EXISTS
// leaves existing tables alone, so they are added here. backfill, when set,
// runs once after the column is added.
var columns = []struct{ table, name, def, backfill string }{
	{"projects", "kits", "TEXT NOT NULL DEFAULT ''", ""},
	{"sandboxes", "kits", "TEXT NOT NULL DEFAULT ''", ""},
	{"projects", "claude_model", "TEXT NOT NULL DEFAULT ''", ""},
	{"sandboxes", "claude_model", "TEXT NOT NULL DEFAULT ''", ""},
	{"projects", "codex_model", "TEXT NOT NULL DEFAULT ''", ""},
	{"sandboxes", "codex_model", "TEXT NOT NULL DEFAULT ''", ""},
	{"projects", "mcps", "TEXT NOT NULL DEFAULT ''", ""},
	{"sandboxes", "mcps", "TEXT NOT NULL DEFAULT ''", ""},
	// Replies from before unread tracking existed count as read.
	{"sandboxes", "seen_at", "INTEGER NOT NULL DEFAULT 0", `UPDATE sandboxes SET seen_at = probed_at`},
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for _, c := range columns {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, c.table, c.name, c.def)); err != nil {
				return err
			}
			if c.backfill != "" {
				if _, err := db.Exec(c.backfill); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// DefaultPath is $XDG_DATA_HOME/sbx-projects/projects.db, falling back to
// ~/.local/share on every platform so the file is easy to find.
func DefaultPath() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "sbx-projects", "projects.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "sbx-projects", "projects.db"), nil
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// A single connection keeps :memory: databases coherent and avoids
	// SQLITE_BUSY between our own goroutines.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, path, remote, default_branch, layout, kits, claude_model, codex_model, mcps, created_at FROM projects ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var kits, mcps string
		var created int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Remote, &p.DefaultBranch, &p.Layout, &kits, &p.ClaudeModel, &p.CodexModel, &mcps, &created); err != nil {
			return nil, err
		}
		p.Kits = splitLines(kits)
		p.MCPs = splitLines(mcps)
		p.CreatedAt = time.Unix(created, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

var ErrExists = errors.New("project already exists")

func (s *Store) AddProject(p Project) (Project, error) {
	if p.Layout == "" {
		p.Layout = LayoutFeature
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	p.CreatedAt = time.Now()
	res, err := s.db.Exec(`INSERT INTO projects (name, path, remote, default_branch, layout, kits, claude_model, codex_model, mcps, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO NOTHING`, p.Name, p.Path, p.Remote, p.DefaultBranch, p.Layout, joinLines(p.Kits), p.ClaudeModel, p.CodexModel, joinLines(p.MCPs), p.CreatedAt.Unix())
	if err != nil {
		return p, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, ErrExists
	}
	p.ID, err = res.LastInsertId()
	return p, err
}

func (s *Store) SetLayout(projectID int64, l Layout) error {
	_, err := s.db.Exec(`UPDATE projects SET layout = ? WHERE id = ?`, l, projectID)
	return err
}

// SetKits replaces the project's default kits. Existing sandboxes keep
// the kits they were created with.
func (s *Store) SetRemote(projectID int64, remote string) error {
	_, err := s.db.Exec(`UPDATE projects SET remote = ? WHERE id = ?`, remote, projectID)
	return err
}

func (s *Store) SetKits(projectID int64, kits []string) error {
	_, err := s.db.Exec(`UPDATE projects SET kits = ? WHERE id = ?`, joinLines(kits), projectID)
	return err
}

func (s *Store) SetClaudeModel(projectID int64, model string) error {
	_, err := s.db.Exec(`UPDATE projects SET claude_model = ? WHERE id = ?`, model, projectID)
	return err
}

func (s *Store) SetCodexModel(projectID int64, model string) error {
	_, err := s.db.Exec(`UPDATE projects SET codex_model = ? WHERE id = ?`, model, projectID)
	return err
}

func (s *Store) SetMCPs(projectID int64, mcps []string) error {
	_, err := s.db.Exec(`UPDATE projects SET mcps = ? WHERE id = ?`, joinLines(mcps), projectID)
	return err
}

// RemoveProject forgets the project and its sandbox records. It never
// touches the sandboxes themselves.
func (s *Store) RemoveProject(projectID int64) error {
	_, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, projectID)
	return err
}

func (s *Store) Sandboxes() ([]Sandbox, error) {
	rows, err := s.db.Query(`SELECT name, project_id, title, branch, agent, clone, kits, claude_model, codex_model, mcps, source, created_at, last_probe, probed_at, seen_at FROM sandboxes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sandbox
	for rows.Next() {
		var sb Sandbox
		var clone int
		var kits, mcps string
		var created, probed, seen int64
		if err := rows.Scan(&sb.Name, &sb.ProjectID, &sb.Title, &sb.Branch, &sb.Agent, &clone, &kits, &sb.ClaudeModel, &sb.CodexModel, &mcps, &sb.Source, &created, &sb.LastProbe, &probed, &seen); err != nil {
			return nil, err
		}
		sb.Clone = clone == 1
		sb.Kits = splitLines(kits)
		sb.MCPs = splitLines(mcps)
		sb.CreatedAt = time.Unix(created, 0)
		if probed > 0 {
			sb.ProbedAt = time.Unix(probed, 0)
		}
		if seen > 0 {
			sb.SeenAt = time.Unix(seen, 0)
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

// UpsertSandbox inserts the record or updates its descriptive fields,
// leaving the cached probe intact.
func (s *Store) UpsertSandbox(sb Sandbox) error {
	if sb.CreatedAt.IsZero() {
		sb.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO sandboxes (name, project_id, title, branch, agent, clone, kits, claude_model, codex_model, mcps, source, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET project_id = excluded.project_id, title = excluded.title, branch = excluded.branch,
			agent = excluded.agent, clone = excluded.clone, kits = excluded.kits,
			claude_model = excluded.claude_model, codex_model = excluded.codex_model, mcps = excluded.mcps, source = excluded.source`,
		sb.Name, sb.ProjectID, sb.Title, sb.Branch, sb.Agent, boolInt(sb.Clone), joinLines(sb.Kits), sb.ClaudeModel, sb.CodexModel, joinLines(sb.MCPs), sb.Source, sb.CreatedAt.Unix())
	return err
}

func (s *Store) SaveProbe(name, probe string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE sandboxes SET last_probe = ?, probed_at = ? WHERE name = ?`, probe, at.Unix(), name)
	return err
}

func (s *Store) SetSeen(name string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE sandboxes SET seen_at = ? WHERE name = ?`, at.Unix(), name)
	return err
}

func (s *Store) SetBranch(name, branch string) error {
	_, err := s.db.Exec(`UPDATE sandboxes SET branch = ? WHERE name = ?`, branch, name)
	return err
}

func (s *Store) RemoveSandbox(name string) error {
	_, err := s.db.Exec(`DELETE FROM sandboxes WHERE name = ?`, name)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Kit references and MCP server names never contain newlines, so one per
// line is unambiguous.
func joinLines(list []string) string { return strings.Join(list, "\n") }

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
