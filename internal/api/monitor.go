package api

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

const (
	probeEvery = 3 * time.Second
	opTimeout  = 15 * time.Minute
)

// listEvery is a variable so tests can poll without waiting.
var listEvery = 3 * time.Second

// Monitor keeps what the TUI keeps for its list, without a terminal: the
// records, what sbx lists, and a probe of each running sandbox every few
// seconds. Subscribers hear about every change.
type Monitor struct {
	svc *core.Service

	mu       sync.Mutex
	projects []store.Project
	recs     map[string]*store.Sandbox
	live     map[string]sbx.Sandbox
	listErr  string
	probes   map[string]*agent.Probe
	probing  map[string]*probeRun
	probeAt  map[string]time.Time
	busy     map[string]string
	// noProbeUntil holds off probes right after a stop or remove.
	noProbeUntil map[string]time.Time
	// sent is the last view of each sandbox subscribers were sent.
	sent map[string]SandboxView
	subs map[chan Event]struct{}
	// kick asks the loop to list again now.
	kick chan struct{}
}

func NewMonitor(svc *core.Service) (*Monitor, error) {
	m := &Monitor{
		svc:  svc,
		recs: map[string]*store.Sandbox{}, live: map[string]sbx.Sandbox{},
		probes: map[string]*agent.Probe{}, probing: map[string]*probeRun{}, probeAt: map[string]time.Time{},
		busy: map[string]string{}, noProbeUntil: map[string]time.Time{},
		sent: map[string]SandboxView{}, subs: map[chan Event]struct{}{}, kick: make(chan struct{}, 1),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// reload reads projects and records from the store. Probes are kept. On
// an error nothing changes.
func (m *Monitor) reload() error {
	ps, err := m.svc.Store.Projects()
	if err != nil {
		return err
	}
	recs, err := m.svc.Store.Sandboxes()
	if err != nil {
		return err
	}
	m.projects = ps
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

// Run lists and probes until ctx is done.
func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(listEvery)
	defer t.Stop()
	for {
		m.list(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.kick:
		}
	}
}

// Refresh lists again soon, as after an action.
func (m *Monitor) Refresh() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Monitor) list(ctx context.Context) {
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	sbxs, err := m.svc.Backend.List(lctx)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	listErr := ""
	switch {
	case errors.Is(err, exec.ErrNotFound):
		listErr = "sbx is not on your PATH"
	case err != nil:
		listErr = err.Error()
	}
	if listErr != m.listErr {
		// Clients show it over the list, whose states are stale until it
		// clears.
		m.listErr = listErr
		m.broadcast(Event{Type: "status", ListError: listErr})
	}
	if err != nil {
		return
	}
	m.live = map[string]sbx.Sandbox{}
	for _, s := range sbxs {
		m.live[s.Name] = s
	}
	// Projects can be added or forgotten from the TUI while the API runs.
	// Forgetting one deletes its sandboxes' records too, so the records are
	// read again with the projects; otherwise the forgotten ones would stay
	// here, and adding the project back couldn't adopt their sandboxes.
	if ps, err := m.svc.Store.Projects(); err == nil && !reflect.DeepEqual(ps, m.projects) && m.reload() == nil {
		m.broadcast(Event{Type: "projects", Projects: m.projectViews()})
	}
	r := m.svc.Reconcile(m.projects, m.recs, sbxs, func(name string) bool { return m.busy[name] != "" })
	for _, name := range r.Removed {
		delete(m.recs, name)
		delete(m.probes, name)
	}
	if r.Adopted > 0 {
		_ = m.reload()
	}

	now := time.Now()
	for name, rec := range m.recs {
		s, ok := m.live[name]
		if !ok || !s.Running() || m.probing[name] != nil || m.busy[name] != "" || now.Before(m.noProbeUntil[name]) {
			continue
		}
		if now.Sub(m.probeAt[name]) < probeEvery-500*time.Millisecond {
			continue
		}
		m.probe(ctx, name, core.ProjectByID(m.projects, rec.ProjectID).DefaultBranch)
	}
	m.publish()
}

// probeRun is a probe in flight. Stopping or removing a sandbox waits it
// out first: sbx exec starts a stopped sandbox, so an exec that reached
// the sandbox after the stop would start it again.
type probeRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// probe reads the sandbox in the background. Callers hold m.mu.
func (m *Monitor) probe(ctx context.Context, name, defBranch string) {
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	run := &probeRun{cancel: cancel, done: make(chan struct{})}
	m.probing[name] = run
	be := m.svc.Backend
	go func() {
		// Closed after the lock below is released, so a waiter can take it.
		defer close(run.done)
		raw, err := be.Exec(pctx, name, agent.ProbeScript(defBranch))
		cancel()

		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.probing, name)
		m.probeAt[name] = time.Now()
		if err != nil {
			return
		}
		p := agent.ParseProbe(raw)
		m.probes[name] = &p
		if rec := m.recs[name]; rec != nil {
			m.svc.SaveProbe(rec, raw, p)
		}
		m.publish()
	}()
}

// drainProbe cancels the sandbox's probe in flight, if there is one, and
// waits for it to finish. Being busy keeps new probes from starting.
func (m *Monitor) drainProbe(name string) {
	m.mu.Lock()
	run := m.probing[name]
	m.mu.Unlock()
	if run != nil {
		run.cancel()
		<-run.done
	}
}

// stops says whether an action stops the sandbox.
func stops(steps []core.Step) bool {
	for _, st := range steps {
		if st.Verb == "Stopping" || st.Verb == "Removing" {
			return true
		}
	}
	return false
}

// run runs an action's steps one after another in the background. Each
// step's busy text is the sandbox's status while it runs. The channel it
// returns gets the action's error, or nil, once it has finished. Callers
// hold m.mu.
func (m *Monitor) run(name string, steps []core.Step, done string) <-chan error {
	m.busy[name] = steps[0].Busy
	m.publish()
	finished := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		if stops(steps) {
			m.drainProbe(name)
		}
		var failed error
		var verb string
		for i, st := range steps {
			if i > 0 {
				m.mu.Lock()
				m.busy[name] = st.Busy
				m.publish()
				m.mu.Unlock()
			}
			out, err := st.Run(ctx)
			if err != nil {
				failed, verb = err, st.Verb
				break
			}
			if st.Apply != nil {
				m.mu.Lock()
				st.Apply(out)
				m.mu.Unlock()
			}
		}

		// List before clearing the busy text, or the sandbox would show
		// its state from before the action until the next listing.
		lctx, lcancel := context.WithTimeout(ctx, 20*time.Second)
		sbxs, lerr := m.svc.Backend.List(lctx)
		lcancel()

		m.mu.Lock()
		defer m.mu.Unlock()
		if lerr == nil {
			m.live = map[string]sbx.Sandbox{}
			for _, s := range sbxs {
				m.live[s.Name] = s
			}
		}
		delete(m.busy, name)
		if failed != nil {
			m.broadcast(Event{Type: "action", Name: name, Message: fmt.Sprintf("%s %s failed: %s", verb, name, failed)})
		} else {
			m.broadcast(Event{Type: "action", Name: name, OK: true, Message: done})
			switch steps[len(steps)-1].Verb {
			case "Stopping", "Removing":
				// A probe now could land on the sandbox while it is going
				// away, and sbx exec would start it again.
				m.noProbeUntil[name] = time.Now().Add(2 * listEvery)
			default:
				if rec := m.recs[name]; rec != nil {
					m.probe(context.Background(), name, core.ProjectByID(m.projects, rec.ProjectID).DefaultBranch)
				}
			}
		}
		m.publish()
		m.Refresh()
		finished <- failed
	}()
	return finished
}

// Subscribe returns a channel of changes, starting with a snapshot of
// everything. cancel ends the subscription.
func (m *Monitor) Subscribe() (events <-chan Event, cancel func()) {
	ch := make(chan Event, 64)
	m.mu.Lock()
	ch <- Event{Type: "snapshot", Projects: m.projectViews(), Sandboxes: m.views(), ListError: m.listErr}
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := m.subs[ch]; ok {
			delete(m.subs, ch)
			close(ch)
		}
	}
}

// broadcast sends ev to every subscriber. A subscriber that has fallen
// behind is dropped; its client reconnects and starts from a new snapshot.
// Callers hold m.mu.
func (m *Monitor) broadcast(ev Event) {
	for ch := range m.subs {
		select {
		case ch <- ev:
		default:
			delete(m.subs, ch)
			close(ch)
		}
	}
}

// publish tells subscribers about every sandbox whose view changed since
// they were last told. Callers hold m.mu.
func (m *Monitor) publish() {
	seen := map[string]bool{}
	for _, v := range m.views() {
		seen[v.Name] = true
		if old, ok := m.sent[v.Name]; ok && reflect.DeepEqual(old, v) {
			continue
		}
		m.sent[v.Name] = v
		v := v
		m.broadcast(Event{Type: "sandbox", Sandbox: &v})
	}
	for name := range m.sent {
		if !seen[name] {
			delete(m.sent, name)
			m.broadcast(Event{Type: "removed", Name: name})
		}
	}
}

// item is everything the views need about one sandbox, as in the TUI.
type item struct {
	rec     *store.Sandbox
	project store.Project
	live    sbx.Sandbox
	listed  bool
	probe   *agent.Probe
	status  agent.Status
	busy    string
	agentUp bool
	ws      string
}

func (it item) running() bool { return it.listed && it.live.Running() }

// item is a sandbox's current state. Callers hold m.mu.
func (m *Monitor) item(rec *store.Sandbox) item {
	it := item{rec: rec, project: core.ProjectByID(m.projects, rec.ProjectID), probe: m.probes[rec.Name], busy: m.busy[rec.Name]}
	it.live, it.listed = m.live[rec.Name]
	it.ws = it.live.Workspace()
	if it.ws == "" {
		it.ws = it.project.Path
	}
	probe := it.probe
	if !it.running() {
		probe = nil
	} else if probe != nil {
		it.agentUp = probe.AgentUp
	}
	it.status = agent.Derive(it.running(), probe, rec.Agent, it.ws)
	if it.busy != "" {
		it.status = agent.Status{State: agent.StateStarting, Text: it.busy}
	}
	return it
}

// lookup finds a sandbox by name. Callers hold m.mu.
func (m *Monitor) lookup(name string) (item, bool) {
	rec := m.recs[name]
	if rec == nil {
		return item{}, false
	}
	return m.item(rec), true
}

// views lists every sandbox, newest first. Callers hold m.mu.
func (m *Monitor) views() []SandboxView {
	out := make([]SandboxView, 0, len(m.recs))
	for _, rec := range m.recs {
		out = append(out, sandboxView(m.item(rec)))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// projectViews lists the projects. Callers hold m.mu.
func (m *Monitor) projectViews() []ProjectView {
	out := make([]ProjectView, 0, len(m.projects))
	for _, p := range m.projects {
		out = append(out, projectView(p))
	}
	return out
}
