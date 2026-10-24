package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cmrigney/sbx-projects/internal/core"
)

// NewProjectRequest is the New project form. Apps name the project, never
// a path: it is made in the projects folder, so a paired device can't
// write anywhere else on the Mac.
type NewProjectRequest struct {
	// Name is the project's folder. It may be left out with a CloneURL,
	// which then names it.
	Name     string `json:"name"`
	CloneURL string `json:"cloneUrl"`
	// Publish creates a private GitHub repo for a new project.
	Publish bool `json:"publish"`
	// Task, when set, starts a first sandbox on the new project with it,
	// as an agent on a private clone of main.
	Task  string `json:"task"`
	Agent string `json:"agent"`
}

// NewProjectView is what making a project answers with.
type NewProjectView struct {
	Project ProjectView  `json:"project"`
	Sandbox *SandboxView `json:"sandbox,omitempty"`
	// Warning is a step that failed after the project was added, such as
	// publishing it.
	Warning string `json:"warning,omitempty"`
}

// ProjectOptionsView is what the New project form offers.
type ProjectOptionsView struct {
	// Dir is where new projects go, with ~ for the home folder.
	Dir string `json:"dir"`
	// CanPublish says whether gh is installed and signed in on the Mac.
	CanPublish bool `json:"canPublish"`
}

// projectsDir is where new projects go. Callers hold m.mu.
func (s *Server) projectsDir() string {
	if s.ProjectsDir != "" {
		return s.ProjectsDir
	}
	return core.ProjectsDir(s.Monitor.projects)
}

func (s *Server) projectOptions(w http.ResponseWriter, r *http.Request) {
	m := s.Monitor
	m.mu.Lock()
	dir, demo := s.projectsDir(), m.svc.Demo
	m.mu.Unlock()
	v := ProjectOptionsView{Dir: tilde(dir), CanPublish: demo}
	if !demo {
		if _, err := exec.LookPath("gh"); err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			v.CanPublish = exec.CommandContext(ctx, "gh", "auth", "status").Run() == nil
			cancel()
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// newProject makes the project before answering, so the form can show
// what went wrong. Making it can take a while for a clone, so it isn't
// tied to the request: a phone that locks mid-clone doesn't cut it off.
func (s *Server) newProject(w http.ResponseWriter, r *http.Request) {
	var req NewProjectRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	name, url := strings.TrimSpace(req.Name), strings.TrimSpace(req.CloneURL)
	if name == "" && url != "" {
		name = core.RepoName(url)
	}
	if err := core.CheckProjectName(name); err != nil {
		writeErr(w, fail(http.StatusBadRequest, "%s", capitalize(err.Error())))
		return
	}
	task := strings.TrimSpace(req.Task)
	creq := CreateRequest{Agent: req.Agent, Source: "branch", Task: task, Clone: true}
	if creq.Agent == "" {
		creq.Agent = "claude"
	}
	if task != "" {
		if err := checkCreate(&creq); err != nil {
			writeErr(w, err)
			return
		}
	}

	// One at a time, so two requests for the same name can't both pass
	// the check for an existing folder.
	s.making.Lock()
	defer s.making.Unlock()
	m := s.Monitor
	m.mu.Lock()
	path := filepath.Join(s.projectsDir(), name)
	m.mu.Unlock()
	p, steps, err := m.svc.NewProject(core.NewProjectRequest{Path: path, CloneURL: url, Publish: req.Publish})
	if err != nil {
		writeErr(w, fail(http.StatusBadRequest, "%s", capitalize(err.Error())))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	var failed error
	var verb string
	for _, st := range steps {
		out, err := st.Run(ctx)
		if err != nil {
			failed, verb = err, st.Verb
			break
		}
		if st.Apply != nil {
			st.Apply(out)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if p.ID == 0 {
		writeErr(w, fail(http.StatusUnprocessableEntity, "%s %s failed: %s", verb, name, failed))
		return
	}
	if err := m.reload(); err != nil {
		writeErr(w, err)
		return
	}
	m.broadcast(Event{Type: "projects", Projects: m.projectViews()})
	v := NewProjectView{Project: projectView(*p)}
	if failed != nil {
		v.Warning = verb + " " + name + " failed: " + failed.Error()
	} else if task != "" {
		creq.ProjectID = p.ID
		sv, err := s.startSandbox(creq)
		if err != nil {
			v.Warning = err.Error()
		} else {
			v.Sandbox = &sv
		}
	}
	m.Refresh()
	writeJSON(w, http.StatusCreated, v)
}

func tilde(p string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		if p == h {
			return "~"
		}
		if strings.HasPrefix(p, h+string(filepath.Separator)) {
			return "~" + p[len(h):]
		}
	}
	return p
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
