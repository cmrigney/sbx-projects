package sbx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mock is an in-memory Backend for demos and development without a daemon.
// It understands the scripts the agent package sends closely enough to
// simulate an agent making progress.
type Mock struct {
	mu   sync.Mutex
	sbxs map[string]*mockSandbox
	ord  []string
	mcps []MCPServer
}

type mockSandbox struct {
	Sandbox
	agentUp bool
	clone   bool
	kits    []string
	mcps    []string
	branch  string
	events  []map[string]any
	commits []string
	files   []string
	script  []map[string]any
}

type MockSeed struct {
	Name, Agent, Workspace, Branch string
	Running, AgentUp, Clone        bool
	CPUs                           int
	MemoryMiB                      int64
	Ports                          []Port
	Age                            time.Duration
	Events                         []map[string]any
	Commits                        []string
	Files                          []string
	// Script is replayed one event per probe while the agent is up.
	Script []map[string]any
}

func NewMock(seeds []MockSeed) *Mock {
	m := &Mock{sbxs: map[string]*mockSandbox{}, mcps: []MCPServer{
		{Name: "notion", Transport: "streamable-http", Status: "ready"},
		{Name: "github", Transport: "stdio", Status: "ready"},
		{Name: "linear", Transport: "streamable-http", Status: "needs-auth", NextStep: "sbx mcp auth linear"},
	}}
	for _, s := range seeds {
		created := time.Now().Add(-s.Age - time.Hour)
		used := time.Now().Add(-s.Age)
		status := "stopped"
		if s.Running {
			status = "running"
		}
		ms := &mockSandbox{
			Sandbox: Sandbox{Name: s.Name, Agent: s.Agent, Status: status, Workspaces: []string{s.Workspace},
				CPUs: s.CPUs, MemoryMiB: s.MemoryMiB, Ports: s.Ports, CreatedAt: &created, LastUsedAt: &used, Detached: true},
			agentUp: s.AgentUp && s.Running, clone: s.Clone, branch: s.Branch,
			events: s.Events, commits: s.Commits, files: s.Files, script: s.Script,
		}
		m.sbxs[s.Name] = ms
		m.ord = append(m.ord, s.Name)
	}
	return m
}

func (m *Mock) List(context.Context) ([]Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Sandbox, 0, len(m.ord))
	for _, n := range m.ord {
		out = append(out, m.sbxs[n].Sandbox)
	}
	return out, nil
}

func (m *Mock) MCPServers(ctx context.Context) ([]MCPServer, error) {
	if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MCPServer(nil), m.mcps...), nil
}

// SetMCPServers replaces the registered MCP servers.
func (m *Mock) SetMCPServers(list []MCPServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mcps = list
}

func (m *Mock) Create(ctx context.Context, o CreateOptions) error {
	if err := sleepCtx(ctx, 1200*time.Millisecond); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sbxs[o.Name]; ok {
		return fmt.Errorf("sbx run: sandbox %q already exists", o.Name)
	}
	now := time.Now()
	m.sbxs[o.Name] = &mockSandbox{
		Sandbox: Sandbox{Name: o.Name, Agent: o.Agent, Status: "running", Workspaces: []string{o.Path}, CPUs: 4, MemoryMiB: 8192,
			CreatedAt: &now, LastUsedAt: &now, Detached: true},
		clone: o.Clone, kits: o.Kits, mcps: o.MCPs, branch: "main",
		script: demoScript(),
	}
	m.ord = append([]string{o.Name}, m.ord...)
	return nil
}

// Kits reports the kits a sandbox was created with.
func (m *Mock) Kits(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sbxs[name]; ok {
		return s.kits
	}
	return nil
}

// MCPs reports the MCP servers a sandbox was created with.
func (m *Mock) MCPs(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sbxs[name]; ok {
		return s.mcps
	}
	return nil
}

func (m *Mock) Start(ctx context.Context, name string) error {
	if err := sleepCtx(ctx, 600*time.Millisecond); err != nil {
		return err
	}
	return m.with(name, func(s *mockSandbox) { s.Status = "running"; now := time.Now(); s.LastUsedAt = &now })
}

func (m *Mock) Stop(ctx context.Context, name string) error {
	if err := sleepCtx(ctx, 400*time.Millisecond); err != nil {
		return err
	}
	return m.with(name, func(s *mockSandbox) { s.Status = "stopped"; s.agentUp = false })
}

func (m *Mock) Remove(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sbxs[name]; !ok {
		return fmt.Errorf("sbx rm: no such sandbox %q", name)
	}
	delete(m.sbxs, name)
	for i, n := range m.ord {
		if n == name {
			m.ord = append(m.ord[:i], m.ord[i+1:]...)
			break
		}
	}
	return nil
}

func (m *Mock) PublishPort(ctx context.Context, name string, p Port) error {
	if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sbxs[name]
	if !ok {
		return fmt.Errorf("sbx ports: no such sandbox %q", name)
	}
	for _, o := range m.sbxs {
		for _, q := range o.Ports {
			if q.HostPort == p.HostPort && q.Protocol == p.Protocol {
				return fmt.Errorf("sbx ports: host port %d is already published", p.HostPort)
			}
		}
	}
	s.Ports = append(slices.Clone(s.Ports), p)
	return nil
}

func (m *Mock) UnpublishPort(ctx context.Context, name string, p Port) error {
	if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
		return err
	}
	return m.with(name, func(s *mockSandbox) {
		s.Ports = slices.DeleteFunc(slices.Clone(s.Ports), func(q Port) bool { return q == p })
	})
}

var (
	mockB64Re     = regexp.MustCompile(`printf '%s' '([A-Za-z0-9+/=]*)' \| base64 -d`)
	mockDefaultRe = regexp.MustCompile(`DEF='([^']*)'`)
)

func (m *Mock) Exec(ctx context.Context, name, script string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sbxs[name]
	if !ok {
		return "", fmt.Errorf("sbx exec: no such sandbox %q", name)
	}
	if s.Status != "running" {
		return "", fmt.Errorf("sbx exec: sandbox %q is not running", name)
	}
	now := time.Now().Unix()
	switch {
	case strings.Contains(script, "git diff --no-color"):
		return s.patch(), nil
	case strings.Contains(script, "@@agent"):
		def := "main"
		if mm := mockDefaultRe.FindStringSubmatch(script); mm != nil {
			def = mm[1]
		}
		if s.agentUp && len(s.script) > 0 {
			ev := s.script[0]
			s.script = s.script[1:]
			ev["ts"] = now
			s.events = append(s.events, ev)
			if ev["tool"] == "Bash" && strings.Contains(fmt.Sprint(ev["cmd"]), "git commit") {
				s.commits = append(s.commits, fmt.Sprintf("%07x\t%d\t%s", now&0xfffffff, now, commitSubject(fmt.Sprint(ev["cmd"]))))
			}
			if f, ok := ev["file"].(string); ok && (ev["tool"] == "Edit" || ev["tool"] == "Write") {
				s.files = append(s.files, fmt.Sprintf("%d\t%d\t%s", 10+len(s.files)*7, len(s.files)*3, strings.TrimPrefix(f, s.Workspace()+"/")))
			}
		}
		return s.probe(def), nil
	case strings.Contains(script, "new-session"):
		if !s.agentUp {
			s.agentUp = true
			// Like the hook, a fresh session starts the activity over.
			reason := "resume"
			if !strings.Contains(script, "--continue") && !strings.Contains(script, "resume --last") {
				reason, s.events = "startup", nil
			}
			s.events = append(s.events, map[string]any{"ts": now, "ev": "SessionStart", "reason": reason})
			if p := decodeB64(script); p != "" {
				s.events = append(s.events, map[string]any{"ts": now, "ev": "UserPromptSubmit", "prompt": p})
				if len(s.script) == 0 {
					s.script = demoScript()
				}
			}
		}
		return "started\n", nil
	case strings.Contains(script, "send-keys"):
		if !s.agentUp {
			return "", fmt.Errorf("sbx exec: agent is not running")
		}
		p := decodeB64(script)
		s.events = append(s.events, map[string]any{"ts": now, "ev": "UserPromptSubmit", "prompt": p})
		if strings.Contains(strings.ToLower(p), "pull request") {
			s.script = append(s.script,
				map[string]any{"ev": "PreToolUse", "tool": "Bash", "cmd": "git push -u origin " + s.branch, "desc": "Push the branch"},
				map[string]any{"ev": "PreToolUse", "tool": "Bash", "cmd": "gh pr create --draft --fill", "desc": "Open a draft PR"},
				map[string]any{"ev": "Stop", "msg": "Opened draft PR #4140 for " + s.branch})
		} else {
			s.script = append(s.script, demoScript()...)
		}
		return "", nil
	case strings.Contains(script, "gh pr checkout"), strings.Contains(script, "git checkout"):
		v := decodeB64(script)
		if strings.Contains(script, "gh pr checkout") {
			v = "pr/" + strings.TrimLeft(v, "#")
		}
		s.branch = v
		return v + "\n", nil
	}
	return "", nil
}

func (s *mockSandbox) probe(def string) string {
	var b strings.Builder
	b.WriteString("@@agent\n")
	if s.agentUp {
		b.WriteString("up\n")
	} else {
		b.WriteString("down\n")
	}
	b.WriteString("@@clone\n")
	if s.clone {
		b.WriteString("yes\n")
	} else {
		b.WriteString("no\n")
	}
	b.WriteString("@@events\n")
	for _, e := range s.events {
		j, _ := json.Marshal(e)
		b.Write(j)
		b.WriteByte('\n')
	}
	// The hook keeps the last reply whole; here it comes from the newest
	// Stop event.
	for i := len(s.events) - 1; i >= 0; i-- {
		if e := s.events[i]; e["ev"] == "Stop" && e["msg"] != nil {
			fmt.Fprintf(&b, "@@recap\n%v\n%v\n", e["ts"], e["msg"])
			break
		}
	}
	fmt.Fprintf(&b, "@@branch\n%s\n@@base\norigin/%s\n@@commits\n", s.branch, def)
	for i := len(s.commits) - 1; i >= 0; i-- {
		b.WriteString(s.commits[i] + "\n")
	}
	b.WriteString("@@diff\n")
	for _, f := range s.files {
		b.WriteString(f + "\n")
	}
	return b.String()
}

// patch makes up a unified diff that matches the numstat in s.files.
func (s *mockSandbox) patch() string {
	var b strings.Builder
	for _, f := range s.files {
		parts := strings.SplitN(f, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		add, _ := strconv.Atoi(parts[0])
		del, _ := strconv.Atoi(parts[1])
		path := parts[2]
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", path, path)
		if del == 0 {
			fmt.Fprintf(&b, "new file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, add)
		} else {
			fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@ -10,%d +10,%d @@ func example() {\n", path, path, del+2, add+2)
			b.WriteString(" \t// unchanged context\n")
		}
		for i := 0; i < del; i++ {
			fmt.Fprintf(&b, "-\told line %d\n", i+1)
		}
		for i := 0; i < add; i++ {
			fmt.Fprintf(&b, "+\tnew line %d\n", i+1)
		}
		if del > 0 {
			b.WriteString(" }\n")
		}
	}
	return b.String()
}

func (m *Mock) Interactive(name string, argv ...string) *exec.Cmd {
	msg := fmt.Sprintf("demo: sbx exec -it %s %s\n\nThis is where the agent's session would take over the terminal.\nPress enter to return.", name, strings.Join(argv, " "))
	return exec.Command("sh", "-c", `clear; printf '%s\n' "$1"; read _`, "sh", msg)
}

func (m *Mock) with(name string, fn func(*mockSandbox)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sbxs[name]
	if !ok {
		return fmt.Errorf("no such sandbox %q", name)
	}
	fn(s)
	return nil
}

func decodeB64(script string) string {
	mm := mockB64Re.FindStringSubmatch(script)
	if mm == nil {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(mm[1])
	if err != nil {
		return ""
	}
	return string(b)
}

func commitSubject(cmd string) string {
	if i := strings.Index(cmd, "-m \""); i >= 0 {
		rest := cmd[i+4:]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			return rest[:j]
		}
	}
	return "wip"
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func demoScript() []map[string]any {
	return []map[string]any{
		{"ev": "PreToolUse", "tool": "Grep", "pattern": "Installation"},
		{"ev": "PreToolUse", "tool": "Read", "file": "README.md"},
		{"ev": "PreToolUse", "tool": "Read", "file": "LICENSE"},
		{"ev": "PreToolUse", "tool": "Edit", "file": "README.md"},
		{"ev": "PreToolUse", "tool": "Bash", "cmd": "git diff --check", "desc": "Check the documentation diff"},
		{"ev": "PreToolUse", "tool": "Edit", "file": "README.md"},
		{"ev": "PreToolUse", "tool": "Bash", "cmd": "git commit -am \"docs: clarify project setup\"", "desc": "Commit the change"},
		{"ev": "Stop", "msg": "Clarified the setup instructions and checked the documentation diff."},
	}
}

// MockProbe renders the probe output a seed would produce, for caching
// the last known state of sandboxes that start out stopped.
func MockProbe(s MockSeed) string {
	ms := &mockSandbox{clone: s.Clone, branch: s.Branch, events: s.Events, commits: s.Commits, files: s.Files}
	return ms.probe("main")
}
