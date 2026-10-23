package core

import (
	"time"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

// Reconciled is what Reconcile changed.
type Reconciled struct {
	// Removed are records dropped because sbx no longer lists them.
	Removed []string
	// Adopted counts sandboxes found inside a project and recorded, and
	// sandboxes another sbx-projects process recorded first. Either way the
	// caller should reload its records.
	Adopted int
	// Loose are listed sandboxes that belong to no project.
	Loose []sbx.Sandbox
}

// Reconcile brings the store in line with what sbx lists: records of
// sandboxes that are gone are dropped, unless keep says they are being
// worked on, and sandboxes whose workspace is inside a project are adopted.
// recs is read, not changed.
func (s *Service) Reconcile(projects []store.Project, recs map[string]*store.Sandbox, live []sbx.Sandbox, keep func(name string) bool) Reconciled {
	var r Reconciled
	listed := map[string]bool{}
	for _, l := range live {
		listed[l.Name] = true
	}
	for name := range recs {
		if !listed[name] && (keep == nil || !keep(name)) {
			warn(s.Store.RemoveSandbox(name), "forget a sandbox sbx no longer lists", "sandbox", name)
			r.Removed = append(r.Removed, name)
		}
	}
	// stored is loaded the first time a listed sandbox isn't in recs. The
	// TUI and the API keep their own records, so the other one may have
	// recorded it, and adopting it would overwrite its title and settings.
	var stored map[string]bool
	for _, l := range live {
		if _, ok := recs[l.Name]; ok {
			continue
		}
		if stored == nil {
			stored = map[string]bool{}
			if all, err := s.Store.Sandboxes(); err == nil {
				for _, rec := range all {
					stored[rec.Name] = true
				}
			}
		}
		if stored[l.Name] {
			r.Adopted++
			continue
		}
		if p := ProjectFor(projects, l.Workspace()); p != nil {
			rec := store.Sandbox{Name: l.Name, ProjectID: p.ID, Title: l.Name, Agent: l.Agent, Source: "adopted"}
			if l.CreatedAt != nil {
				rec.CreatedAt = *l.CreatedAt
			}
			if err := s.Store.UpsertSandbox(rec); err != nil {
				warn(err, "adopt a sandbox", "sandbox", l.Name, "project", p.Name)
			} else {
				r.Adopted++
			}
			continue
		}
		r.Loose = append(r.Loose, l)
	}
	return r
}

// SaveProbe keeps the probe for when the sandbox is stopped, and updates
// what the probe says about the record: its branch, and for adopted
// sandboxes, whether it is a private clone.
func (s *Service) SaveProbe(rec *store.Sandbox, raw string, p agent.Probe) {
	if rec.LastProbe != raw {
		rec.LastProbe = raw
		warn(s.Store.SaveProbe(rec.Name, raw, time.Now()), "save the probe", "sandbox", rec.Name)
	}
	changed := false
	if p.Branch != "" && p.Branch != rec.Branch {
		rec.Branch, changed = p.Branch, true
	}
	// Only sandboxes made outside this app have an unknown workspace mode.
	if rec.Source == "adopted" && p.Clone != rec.Clone {
		rec.Clone, changed = p.Clone, true
	}
	if changed {
		warn(s.Store.UpsertSandbox(*rec), "save what the probe found", "sandbox", rec.Name)
	}
}

// Unread reports whether the agent has replied since the user last read
// its reply or attached.
func Unread(rec *store.Sandbox, p *agent.Probe) bool {
	return rec != nil && p != nil && p.Recap != nil && p.Recap.When.After(rec.SeenAt)
}

// MarkSeen records that the user has read everything the agent has said
// so far. The reply's own time is used when it is later than now, as it
// comes from the sandbox's clock.
func (s *Service) MarkSeen(rec *store.Sandbox, p *agent.Probe) {
	at := time.Now()
	if p != nil && p.Recap != nil && p.Recap.When.After(at) {
		at = p.Recap.When
	}
	rec.SeenAt = at
	warn(s.Store.SetSeen(rec.Name, at), "mark the reply read", "sandbox", rec.Name)
}
