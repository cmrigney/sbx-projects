// Package api serves sbx-projects over HTTPS so the iOS app can show and
// drive the same sandboxes as the TUI. Every action goes through
// internal/core, as the TUI's do.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

// Version is the API's version, which the app checks before using it.
const Version = 1

type Server struct {
	Monitor *Monitor
	Devices *Devices
	// ID is the start of the certificate's fingerprint, which tells apart
	// two Macs with the same name.
	ID   string
	Name string
	// ProjectsDir is where projects made from the apps go. Empty means
	// ~/Projects.
	ProjectsDir string

	// making holds one new project at a time.
	making sync.Mutex
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/server", s.server)
	mux.HandleFunc("POST /v1/pair", s.pair)
	// The terminal authenticates itself, as browsers send its token in the
	// WebSocket's subprotocols.
	mux.HandleFunc("GET /v1/sandboxes/{name}/terminal", s.terminal)

	auth := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.auth(h)) }
	auth("GET /v1/events", s.events)
	auth("GET /v1/projects", s.projects)
	auth("POST /v1/projects", s.newProject)
	auth("GET /v1/project-options", s.projectOptions)
	auth("GET /v1/options", s.options)
	auth("GET /v1/sandboxes", s.sandboxes)
	auth("POST /v1/sandboxes", s.create)
	auth("GET /v1/sandboxes/{name}", s.sandbox)
	auth("DELETE /v1/sandboxes/{name}", s.remove)
	auth("GET /v1/sandboxes/{name}/reply", s.reply)
	auth("POST /v1/sandboxes/{name}/seen", s.seen)
	auth("GET /v1/sandboxes/{name}/diff", s.diff)
	auth("POST /v1/sandboxes/{name}/messages", s.message)
	auth("POST /v1/sandboxes/{name}/pr", s.openPR)
	auth("POST /v1/sandboxes/{name}/stop", s.stop)
	auth("POST /v1/sandboxes/{name}/resume", s.resume)
	auth("POST /v1/sandboxes/{name}/checkout", s.checkout)
	return mux
}

// apiError is an error with the HTTP status it is answered with.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func fail(status int, format string, a ...any) error {
	return &apiError{status, fmt.Sprintf(format, a...)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		ae = &apiError{http.StatusInternalServerError, err.Error()}
	}
	writeJSON(w, ae.status, map[string]string{"error": ae.msg})
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v); err != nil {
		return fail(http.StatusBadRequest, "The request isn't valid JSON: %s", err)
	}
	return nil
}

func (s *Server) auth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		dev, ok := s.Devices.Authenticate(token)
		if !ok {
			writeErr(w, fail(http.StatusUnauthorized, "This device isn't paired with %s. Pair it again with sbx-projects api --pair.", s.Name))
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, dev.ID)))
	})
}

// deviceKey holds the ID of the device a request authenticated as.
type deviceKey struct{}

// recheckEvery is how often an open event stream checks that its device is
// still paired, so signing a phone out also ends the stream it has open.
// It is a variable so tests need not wait.
var recheckEvery = 5 * time.Second

func (s *Server) server(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"id": s.ID, "name": s.Name, "version": Version, "pairing": s.Devices.Pairing()})
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code   string `json:"code"`
		Device string `json:"device"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	dev, token, err := s.Devices.Pair(req.Code, req.Device)
	switch {
	case errors.Is(err, ErrNotPairing):
		writeErr(w, fail(http.StatusForbidden, "%s", err))
	case errors.Is(err, ErrWrongCode):
		writeErr(w, fail(http.StatusUnauthorized, "%s", err))
	case err != nil:
		writeErr(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "deviceId": dev.ID, "server": map[string]any{"id": s.ID, "name": s.Name}})
	}
}

// events streams changes as server-sent events. Each event is a single
// "data:" line of JSON, so clients that read line by line need not track
// the blank lines between events. The first is a snapshot of everything.
// The stream ends when its device is signed out; the client's reconnect is
// then refused.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	device, _ := r.Context().Value(deviceKey{}).(string)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, cancel := s.Monitor.Subscribe()
	defer cancel()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	recheck := time.NewTicker(recheckEvery)
	defer recheck.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-recheck.C:
			if !s.Devices.Has(device) {
				return
			}
			continue
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	m := s.Monitor
	m.mu.Lock()
	v := m.projectViews()
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) sandboxes(w http.ResponseWriter, r *http.Request) {
	m := s.Monitor
	m.mu.Lock()
	v := m.views()
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

// withSandbox runs fn on the named sandbox while holding the monitor's lock.
func (s *Server) withSandbox(r *http.Request, fn func(it item) error) error {
	m := s.Monitor
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.lookup(r.PathValue("name"))
	if !ok {
		return fail(http.StatusNotFound, "There's no sandbox called %s", r.PathValue("name"))
	}
	return fn(it)
}

func (s *Server) sandbox(w http.ResponseWriter, r *http.Request) {
	var d SandboxDetail
	if err := s.withSandbox(r, func(it item) error { d = sandboxDetail(it); return nil }); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) reply(w http.ResponseWriter, r *http.Request) {
	var v ReplyView
	err := s.withSandbox(r, func(it item) error {
		if it.probe == nil || it.probe.Recap == nil {
			if !agent.ReportsActivity(it.rec.Agent) {
				return fail(http.StatusNotFound, "%s doesn't report its replies", core.AgentLabel(it.rec.Agent))
			}
			return fail(http.StatusNotFound, "No reply saved yet. It appears after the agent's next turn")
		}
		v = ReplyView{At: unix(it.probe.Recap.When), Markdown: it.probe.Recap.Text}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// seen clears the unread mark, as reading the reply or attaching does in
// the TUI.
func (s *Server) seen(w http.ResponseWriter, r *http.Request) {
	var v SandboxView
	err := s.withSandbox(r, func(it item) error {
		s.Monitor.svc.MarkSeen(it.rec, it.probe)
		s.Monitor.publish()
		v = sandboxView(s.Monitor.item(it.rec))
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) diff(w http.ResponseWriter, r *http.Request) {
	var run func(ctx context.Context) (string, error)
	err := s.withSandbox(r, func(it item) error {
		if it.probe == nil || len(it.probe.Files) == 0 {
			return nil
		}
		var err error
		run, err = s.Monitor.svc.Diff(it.rec, it.project, it.live, it.running())
		if errors.Is(err, core.ErrNeedsStart) {
			return fail(http.StatusConflict, "Resume the sandbox to see its diff")
		}
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if run == nil {
		writeJSON(w, http.StatusOK, diffView(nil))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := run(ctx)
	if err != nil {
		writeErr(w, fail(http.StatusBadGateway, "Could not read the diff: %s", err))
		return
	}
	writeJSON(w, http.StatusOK, diffView(agent.ParseDiff(raw)))
}

// act starts an action on the named sandbox and answers with its view,
// which shows the action as the sandbox's status. How it ends arrives as
// an "action" event.
func (s *Server) act(w http.ResponseWriter, r *http.Request, fn func(it item) ([]core.Step, string, error)) {
	var v SandboxView
	err := s.withSandbox(r, func(it item) error {
		if it.busy != "" {
			return fail(http.StatusConflict, "%s is busy: %s", it.rec.Name, it.busy)
		}
		steps, done, err := fn(it)
		if err != nil {
			return err
		}
		s.Monitor.run(it.rec.Name, steps, done)
		v = sandboxView(s.Monitor.item(it.rec))
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (s *Server) message(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	s.act(w, r, func(it item) ([]core.Step, string, error) {
		if text == "" {
			return nil, "", fail(http.StatusBadRequest, "The message is empty")
		}
		return s.Monitor.svc.Send(it.rec, it.running(), it.agentUp, text), "Sent your message to " + it.rec.Name, nil
	})
}

func (s *Server) openPR(w http.ResponseWriter, r *http.Request) {
	s.act(w, r, func(it item) ([]core.Step, string, error) {
		if why := core.PRBlocker(it.project, it.rec, it.probe); why != "" {
			return nil, "", fail(http.StatusConflict, "%s", why)
		}
		prompt := core.PRPrompt(it.project.DefaultBranch)
		return s.Monitor.svc.Send(it.rec, it.running(), it.agentUp, prompt), "Asked " + it.rec.Name + " to open a draft PR", nil
	})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	s.act(w, r, func(it item) ([]core.Step, string, error) {
		if !it.running() {
			return nil, "", fail(http.StatusConflict, "%s is already stopped", it.rec.Name)
		}
		return s.Monitor.svc.Stop(it.rec.Name), "Stopped " + it.rec.Name + ". Its clone and branch are kept.", nil
	})
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	s.act(w, r, func(it item) ([]core.Step, string, error) {
		if it.running() {
			return nil, "", fail(http.StatusConflict, "%s is already running", it.rec.Name)
		}
		return s.Monitor.svc.Resume(it.rec), "Resumed " + it.rec.Name, nil
	})
}

func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	s.act(w, r, func(it item) ([]core.Step, string, error) {
		if !core.CanCheckout(it.rec, it.running()) {
			return nil, "", fail(http.StatusConflict, "Only a running sandbox on a private clone can be checked out")
		}
		return s.Monitor.svc.CheckoutLocal(it.project, it.rec), "Checked out " + it.rec.Branch + " in " + it.project.Name, nil
	})
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	s.act(w, r, func(it item) ([]core.Step, string, error) {
		return s.Monitor.svc.Remove(it.rec.Name, it.running()), "Removed " + it.rec.Name, nil
	})
}

// CreateRequest is the New sandbox form.
type CreateRequest struct {
	ProjectID int64  `json:"projectId"`
	Agent     string `json:"agent"`
	// Source is "branch" or "pr".
	Source string   `json:"source"`
	Value  string   `json:"value"`
	Task   string   `json:"task"`
	Clone  bool     `json:"clone"`
	Kits   []string `json:"kits"`
	Model  string   `json:"model"`
	MCPs   []string `json:"mcps"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := checkCreate(&req); err != nil {
		writeErr(w, err)
		return
	}
	m := s.Monitor
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := s.startSandbox(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

// startSandbox records a checked request's sandbox and starts making it.
// Callers hold m.mu.
func (s *Server) startSandbox(req CreateRequest) (SandboxView, error) {
	m := s.Monitor
	i := slices.IndexFunc(m.projects, func(p store.Project) bool { return p.ID == req.ProjectID })
	if i < 0 {
		return SandboxView{}, fail(http.StatusNotFound, "There's no project with ID %d", req.ProjectID)
	}
	p := &m.projects[i]
	creq := core.CreateRequest{Agent: req.Agent, Source: req.Source, Value: req.Value, Task: req.Task, Clone: req.Clone,
		Kits: req.Kits, Model: req.Model, MCPs: req.MCPs, RememberMCPs: true}
	taken := func(name string) bool {
		_, listed := m.live[name]
		_, recorded := m.recs[name]
		return listed || recorded
	}
	rec, steps, err := m.svc.Create(p, creq, taken)
	if err != nil {
		return SandboxView{}, fmt.Errorf("Could not save the sandbox: %w", err)
	}
	m.recs[rec.Name] = rec
	m.run(rec.Name, steps, "Created "+rec.Name)
	return sandboxView(m.item(rec)), nil
}

// checkCreate validates the form and tidies it for core.Create.
func checkCreate(req *CreateRequest) error {
	if !slices.ContainsFunc(core.Agents, func(a core.Agent) bool { return a.ID == req.Agent }) {
		return fail(http.StatusBadRequest, "%q isn't an agent sbx-projects can run", req.Agent)
	}
	req.Value = strings.TrimSpace(req.Value)
	switch req.Source {
	case "branch":
		// The branch is optional, as in the TUI: without one, the sandbox
		// is named after the project, and a private clone checks out the
		// default branch.
	case "pr":
		if core.Digits(req.Value) == "0" {
			return fail(http.StatusBadRequest, "Give the pull request's number or URL")
		}
		// gh pr checkout needs a private clone, as the TUI's form enforces.
		req.Clone = true
	default:
		return fail(http.StatusBadRequest, "The source must be \"branch\" or \"pr\"")
	}
	models := core.ModelsFor(req.Agent)
	if len(models) == 0 {
		req.Model = ""
	} else if !slices.ContainsFunc(models, func(m core.Model) bool { return m.ID == req.Model }) {
		return fail(http.StatusBadRequest, "%q isn't a model %s offers", req.Model, core.AgentLabel(req.Agent))
	}
	var kits []string
	for _, k := range req.Kits {
		if k = strings.TrimSpace(k); k != "" {
			kits = append(kits, k)
		}
	}
	req.Kits = kits
	return nil
}

func (s *Server) options(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("project"), 10, 64)
	m := s.Monitor
	m.mu.Lock()
	i := slices.IndexFunc(m.projects, func(p store.Project) bool { return p.ID == id })
	var p store.Project
	if i >= 0 {
		p = m.projects[i]
	}
	be := m.svc.Backend
	m.mu.Unlock()
	if i < 0 {
		writeErr(w, fail(http.StatusNotFound, "There's no project with ID %d", id))
		return
	}

	v := OptionsView{Defaults: DefaultsView{Agent: "claude", Clone: true, Kits: orEmpty(p.Kits), MCPs: orEmpty(p.MCPs),
		Models: map[string]string{"claude": p.ClaudeModel, "codex": p.CodexModel}}}
	for _, a := range core.Agents {
		av := AgentView{ID: a.ID, Label: a.Label, Models: []ModelView{}, ReportsActivity: agent.ReportsActivity(a.ID)}
		for _, mo := range core.ModelsFor(a.ID) {
			av.Models = append(av.Models, ModelView{ID: mo.ID, Label: mo.Label})
		}
		v.Agents = append(v.Agents, av)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := be.MCPServers(ctx)
	if err != nil {
		v.MCPError = err.Error()
	} else {
		v.MCPServers = []MCPServerView{}
		for _, ms := range list {
			v.MCPServers = append(v.MCPServers, mcpServerView(ms))
		}
		// Drop remembered picks that are no longer registered, as the form does.
		v.Defaults.MCPs = slices.DeleteFunc(slices.Clone(v.Defaults.MCPs), func(n string) bool {
			return !slices.ContainsFunc(list, func(ms sbx.MCPServer) bool { return ms.Name == n })
		})
	}
	writeJSON(w, http.StatusOK, v)
}

func mcpServerView(ms sbx.MCPServer) MCPServerView {
	return MCPServerView{Name: ms.Name, Ready: ms.Ready(), Status: ms.Status, Detail: ms.Detail, NextStep: ms.NextStep}
}
