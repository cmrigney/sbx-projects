package api

import (
	"time"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/store"
)

// Times are Unix seconds, and zero is left out, so clients need no date
// parsing beyond a number.

// Event is one line of the /v1/events stream.
type Event struct {
	// Type is "snapshot", "sandbox", "removed", "projects", "status" or
	// "action".
	Type string `json:"type"`
	// Projects and Sandboxes are everything, on "snapshot"; Projects alone
	// on "projects".
	Projects  []ProjectView `json:"projects,omitempty"`
	Sandboxes []SandboxView `json:"sandboxes,omitempty"`
	// ListError is why sbx couldn't list sandboxes, on "snapshot" and on
	// "status", which is sent whenever it starts or stops failing. Left out
	// once listing works again.
	ListError string `json:"listError,omitempty"`
	// Sandbox is the new view of one sandbox, on "sandbox".
	Sandbox *SandboxView `json:"sandbox,omitempty"`
	// Name is the sandbox an "removed" or "action" event is about.
	Name string `json:"name,omitempty"`
	// OK and Message say how an action ended, on "action".
	OK      bool   `json:"ok,omitempty"`
	Message string `json:"message,omitempty"`
}

type ProjectView struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Remote        string   `json:"remote,omitempty"`
	DefaultBranch string   `json:"defaultBranch"`
	Layout        string   `json:"layout"`
	Kits          []string `json:"kits"`
}

func projectView(p store.Project) ProjectView {
	return ProjectView{ID: p.ID, Name: p.Name, Path: p.Path, Remote: p.Remote, DefaultBranch: p.DefaultBranch,
		Layout: string(p.Layout), Kits: orEmpty(p.Kits)}
}

// SandboxView is one row of the sandbox list.
type SandboxView struct {
	Name       string `json:"name"`
	ProjectID  int64  `json:"projectId"`
	Title      string `json:"title"`
	Branch     string `json:"branch,omitempty"`
	Agent      string `json:"agent"`
	AgentLabel string `json:"agentLabel"`
	Model      string `json:"model,omitempty"`
	ModelLabel string `json:"modelLabel,omitempty"`
	Clone      bool   `json:"clone"`
	Source     string `json:"source"`

	Running bool `json:"running"`
	AgentUp bool `json:"agentUp"`
	// State is one of "stopped", "starting", "working", "needsYou",
	// "failed", "ready" or "idle".
	State      string `json:"state"`
	StatusText string `json:"statusText"`
	// Busy is set while an action runs on the sandbox.
	Busy   bool `json:"busy"`
	Unread bool `json:"unread"`
	// HasReply says whether the agent's last reply can be read.
	HasReply        bool `json:"hasReply"`
	ReportsActivity bool `json:"reportsActivity"`
	CanPR           bool `json:"canPR"`
	CanCheckout     bool `json:"canCheckout"`

	Added   int `json:"added"`
	Removed int `json:"removed"`
	Files   int `json:"files"`

	CPUs      int        `json:"cpus,omitempty"`
	MemoryMiB int64      `json:"memoryMiB,omitempty"`
	Ports     []PortView `json:"ports"`
	Kits      []string   `json:"kits"`
	MCPs      []string   `json:"mcps"`

	CreatedAt int64 `json:"createdAt"`
	// ActiveAt is the last thing the agent did, or the sandbox was used.
	ActiveAt int64 `json:"activeAt,omitempty"`
}

type PortView struct {
	HostPort    int    `json:"hostPort"`
	SandboxPort int    `json:"sandboxPort"`
	Protocol    string `json:"protocol"`
}

func sandboxView(it item) SandboxView {
	rec := it.rec
	title := rec.Title
	if title == "" {
		title = rec.Name
	}
	v := SandboxView{
		Name: rec.Name, ProjectID: rec.ProjectID, Title: title, Branch: rec.Branch,
		Agent: rec.Agent, AgentLabel: core.AgentLabel(rec.Agent), Clone: rec.Clone, Source: rec.Source,
		Running: it.running(), AgentUp: it.agentUp, State: stateName(it.status.State), StatusText: it.status.Text,
		Busy: it.busy != "", Unread: core.Unread(rec, it.probe), ReportsActivity: agent.ReportsActivity(rec.Agent),
		CanPR: core.CanPR(rec, it.probe) && it.busy == "", CanCheckout: core.CanCheckout(rec, it.running()) && it.busy == "",
		CPUs: it.live.CPUs, MemoryMiB: it.live.MemoryMiB, Ports: []PortView{},
		Kits: orEmpty(rec.Kits), MCPs: orEmpty(rec.MCPs), CreatedAt: rec.CreatedAt.Unix(),
	}
	if m := core.SandboxModel(rec); m != "" {
		v.Model, v.ModelLabel = m, core.ModelLabel(rec.Agent, m)
	}
	for _, p := range it.live.Ports {
		v.Ports = append(v.Ports, PortView{HostPort: p.HostPort, SandboxPort: p.SandboxPort, Protocol: p.Protocol})
	}
	var active time.Time
	if p := it.probe; p != nil {
		v.HasReply = p.Recap != nil
		v.Added, v.Removed = p.Totals()
		v.Files = len(p.Files)
		if len(p.Events) > 0 {
			active = p.Events[len(p.Events)-1].Time()
		}
	}
	if it.live.LastUsedAt != nil && it.live.LastUsedAt.After(active) {
		active = *it.live.LastUsedAt
	}
	v.ActiveAt = unix(active)
	return v
}

// SandboxDetail is the sandbox's view with its Activity, Changes and
// Setup tabs.
type SandboxDetail struct {
	SandboxView
	Activity []FeedView   `json:"activity"`
	Changes  []FileView   `json:"changes"`
	Commits  []CommitView `json:"commits"`
	// Base is the branch the changes are counted against.
	Base      string `json:"base"`
	Workspace string `json:"workspace"`
	// ProbedAt is when a stopped sandbox's activity and changes were last
	// seen.
	ProbedAt int64 `json:"probedAt,omitempty"`
}

type FeedView struct {
	At   int64  `json:"at"`
	Text string `json:"text"`
	Path string `json:"path,omitempty"`
	// Kind is "plain", "you", "good", "warn" or "bad".
	Kind string `json:"kind"`
}

type FileView struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
}

type CommitView struct {
	Hash    string `json:"hash"`
	At      int64  `json:"at"`
	Subject string `json:"subject"`
}

func sandboxDetail(it item) SandboxDetail {
	d := SandboxDetail{SandboxView: sandboxView(it), Activity: []FeedView{}, Changes: []FileView{}, Commits: []CommitView{},
		Base: it.project.DefaultBranch, Workspace: it.ws}
	if !it.running() {
		d.ProbedAt = unix(it.rec.ProbedAt)
	}
	p := it.probe
	if p == nil {
		return d
	}
	if p.Base != "" {
		d.Base = p.Base
	}
	for _, f := range agent.Feed(p.Events, it.ws) {
		d.Activity = append(d.Activity, FeedView{At: unix(f.When), Text: f.Text, Path: f.Path, Kind: feedKind(f.Kind)})
	}
	for _, f := range p.Files {
		d.Changes = append(d.Changes, FileView{Path: f.Path, Added: f.Add, Removed: f.Del, Binary: f.Binary})
	}
	for _, c := range p.Commits {
		d.Commits = append(d.Commits, CommitView{Hash: c.Hash, At: unix(c.When), Subject: c.Subject})
	}
	return d
}

type ReplyView struct {
	At       int64  `json:"at"`
	Markdown string `json:"markdown"`
}

type DiffView struct {
	Files []FileDiffView `json:"files"`
	// Truncated says the diff was cut short to keep the response small.
	Truncated bool `json:"truncated,omitempty"`
}

type FileDiffView struct {
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
	// Lines are the hunks, starting at the first "@@" line.
	Lines []string `json:"lines"`
}

// maxDiffLines caps a diff response; past it, files keep their names but
// lose their hunks.
const maxDiffLines = 20000

func diffView(files []agent.FileDiff) DiffView {
	d := DiffView{Files: []FileDiffView{}}
	n := 0
	for _, f := range files {
		fv := FileDiffView{Path: f.Path, Note: f.Note, Lines: orEmpty(f.Lines)}
		if n+len(f.Lines) > maxDiffLines {
			fv.Lines, d.Truncated = []string{}, true
		}
		n += len(fv.Lines)
		d.Files = append(d.Files, fv)
	}
	return d
}

// OptionsView is what the New sandbox form offers for a project.
type OptionsView struct {
	Agents []AgentView `json:"agents"`
	// MCPServers is nil when sbx couldn't list them; MCPError says why.
	MCPServers []MCPServerView `json:"mcpServers"`
	MCPError   string          `json:"mcpError,omitempty"`
	Defaults   DefaultsView    `json:"defaults"`
}

type AgentView struct {
	ID              string      `json:"id"`
	Label           string      `json:"label"`
	Models          []ModelView `json:"models"`
	ReportsActivity bool        `json:"reportsActivity"`
}

type ModelView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type MCPServerView struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	// NextStep is the command, run on the Mac, that makes the server ready.
	NextStep string `json:"nextStep,omitempty"`
}

type DefaultsView struct {
	Agent string   `json:"agent"`
	Clone bool     `json:"clone"`
	Kits  []string `json:"kits"`
	MCPs  []string `json:"mcps"`
	// Models is the model last picked for each agent that has a choice.
	Models map[string]string `json:"models"`
}

func stateName(s agent.State) string {
	switch s {
	case agent.StateStarting:
		return "starting"
	case agent.StateWorking:
		return "working"
	case agent.StateNeedsYou:
		return "needsYou"
	case agent.StateFailed:
		return "failed"
	case agent.StateReady:
		return "ready"
	case agent.StateIdle:
		return "idle"
	}
	return "stopped"
}

func feedKind(k agent.FeedKind) string {
	switch k {
	case agent.FeedYou:
		return "you"
	case agent.FeedGood:
		return "good"
	case agent.FeedWarn:
		return "warn"
	case agent.FeedBad:
		return "bad"
	}
	return "plain"
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// orEmpty keeps lists as [] rather than null in JSON.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
