package core

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

// CreateRequest is what the New sandbox form asks for.
type CreateRequest struct {
	Agent string
	// Source is "branch", "pr" or "repo" (the project's one shared sandbox).
	Source string
	// Value is the branch name or the PR number or URL.
	Value string
	// Task is the agent's first instruction, and the sandbox's title.
	Task  string
	Clone bool
	Kits  []string
	// Model is the agent's --model; empty keeps its default.
	Model string
	// MCPs are passed to sbx run as --static-mcp.
	MCPs []string
	// RememberMCPs makes MCPs the project's pick for its next sandbox.
	RememberMCPs bool
}

// Create records a new sandbox and returns the steps that make it: sbx
// run, then the branch in a private clone, then the agent. taken reports
// sandbox names already in use besides those on record. p is updated with what the project
// remembers for next time.
func (s *Service) Create(p *store.Project, r CreateRequest, taken func(string) bool) (*store.Sandbox, []Step, error) {
	recs, err := s.Store.Sandboxes()
	if err != nil {
		return nil, nil, err
	}
	used := map[string]bool{}
	for _, sb := range recs {
		used[sb.Name] = true
	}
	name := uniqueName(SandboxName(r.Agent, p.Name, r.Source, r.Value), func(n string) bool {
		return used[n] || (taken != nil && taken(n))
	})
	branch := strings.TrimSpace(r.Value)
	title := strings.TrimSpace(r.Task)
	switch {
	case r.Source == "repo":
		branch, title = "", "Working sandbox for "+p.Name
	case title == "" && r.Source == "pr":
		title = "Review PR " + branch
	case title == "":
		title = branch
	}
	rec := &store.Sandbox{Name: name, ProjectID: p.ID, Title: title, Branch: branch, Agent: r.Agent, Clone: r.Clone,
		Kits: r.Kits, MCPs: r.MCPs, Source: r.Source, CreatedAt: time.Now()}
	switch r.Agent {
	case "claude":
		rec.ClaudeModel = r.Model
		if r.Model != p.ClaudeModel {
			p.ClaudeModel = r.Model
			_ = s.Store.SetClaudeModel(p.ID, r.Model)
		}
	case "codex":
		rec.CodexModel = r.Model
		if r.Model != p.CodexModel {
			p.CodexModel = r.Model
			_ = s.Store.SetCodexModel(p.ID, r.Model)
		}
	}
	if r.RememberMCPs && !slices.Equal(r.MCPs, p.MCPs) {
		p.MCPs = r.MCPs
		_ = s.Store.SetMCPs(p.ID, r.MCPs)
	}
	if err := s.Store.UpsertSandbox(*rec); err != nil {
		return nil, nil, err
	}
	if p.Layout == store.LayoutRepo && r.Clone {
		p.Layout = store.LayoutFeature
		_ = s.Store.SetLayout(p.ID, p.Layout)
	}

	prompt := strings.TrimSpace(r.Task)
	if r.Source == "pr" && prompt == "" {
		prompt = ReviewPrompt
	}
	be, st := s.Backend, s.Store
	opts := sbx.CreateOptions{Name: name, Agent: r.Agent, Path: p.Path, Clone: r.Clone, Kits: r.Kits, MCPs: r.MCPs}
	steps := []Step{{Busy: "Creating the sandbox", Verb: "Creating", Run: func(ctx context.Context) (string, error) {
		return "", be.Create(ctx, opts)
	}}}
	if r.Clone && branch != "" && (r.Source == "branch" || r.Source == "pr") {
		script := agent.GitSetupScript(r.Source, branch, p.DefaultBranch)
		steps = append(steps, Step{Busy: "Checking out " + branch, Verb: "Checking out a branch in",
			Run: func(ctx context.Context) (string, error) {
				out, err := be.Exec(ctx, name, script)
				if err != nil {
					return "", err
				}
				b := strings.TrimSpace(lastLine(out))
				if b != "" {
					_ = st.SetBranch(name, b)
				}
				return b, nil
			},
			Apply: func(b string) {
				if b != "" {
					rec.Branch = b
				}
			}})
	}
	steps = append(steps, s.StartAgent(rec, prompt, false))
	return rec, steps, nil
}
