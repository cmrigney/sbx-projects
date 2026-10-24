// Package core holds what sbx-projects does with projects and sandboxes,
// apart from how it is shown. The TUI drives it, and so can anything else
// that needs the same naming, create flow and bookkeeping.
//
// Multi-step actions are returned as Steps rather than run here, so a
// caller can show progress between them and run each one where it likes.
package core

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

type Service struct {
	Backend sbx.Backend
	Store   *store.Store
	// Demo keeps reads off the host, whose paths demo data makes up.
	Demo bool
}

// Step is one part of an action.
type Step struct {
	// Busy describes the step while it runs, as in "Starting the sandbox".
	Busy string
	// Verb names the step in errors: "<Verb> <sandbox> failed".
	Verb string
	// Run does the work. It may run on any goroutine, so it must not touch
	// records the caller can see.
	Run func(ctx context.Context) (string, error)
	// Apply, when set, takes Run's output once it has succeeded. Callers run
	// it on the goroutine that owns their records.
	Apply func(out string)
}

// warn logs a store write that failed without stopping what made it.
func warn(err error, what string, args ...any) {
	if err != nil {
		slog.Warn(what, append(args, "err", err)...)
	}
}

// Run runs steps in order, stopping at the first error.
func Run(ctx context.Context, steps []Step) error {
	for _, s := range steps {
		out, err := s.Run(ctx)
		if err != nil {
			return err
		}
		if s.Apply != nil {
			s.Apply(out)
		}
	}
	return nil
}

// ProjectFor is the project whose path holds ws, the deepest one when
// projects nest.
func ProjectFor(projects []store.Project, ws string) *store.Project {
	var best *store.Project
	for i := range projects {
		p := &projects[i]
		if ws == p.Path || strings.HasPrefix(ws, strings.TrimSuffix(p.Path, "/")+"/") {
			if best == nil || len(p.Path) > len(best.Path) {
				best = p
			}
		}
	}
	return best
}

// ProjectByID falls back to a project on "main" so callers always have a
// default branch to compare against.
func ProjectByID(projects []store.Project, id int64) store.Project {
	for _, p := range projects {
		if p.ID == id {
			return p
		}
	}
	return store.Project{DefaultBranch: "main"}
}

// CanPR says whether the sandbox has commits worth a pull request.
func CanPR(proj store.Project, rec *store.Sandbox, p *agent.Probe) bool {
	return PRBlocker(proj, rec, p) == ""
}

// PRBlocker says why a pull request can't be opened, or "" when it can.
func PRBlocker(proj store.Project, rec *store.Sandbox, p *agent.Probe) string {
	switch {
	case proj.Remote == "":
		return "The project has no remote to open a pull request on"
	case !rec.Clone:
		return "Pull requests are opened from clone-mode sandboxes"
	case p == nil:
		return "No changes recorded yet"
	case len(p.Commits) == 0:
		return "No commits to open a pull request for"
	}
	return ""
}

// CanCheckout says whether the sandbox's branch can be fetched into the
// project's checkout, which needs sandboxd's remote for a running clone.
func CanCheckout(rec *store.Sandbox, running bool) bool {
	return rec.Clone && rec.Branch != "" && running
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
