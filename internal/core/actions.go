package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

const prPrompt = "Push this branch to origin and open a draft pull request against %s with `gh pr create --draft`. Write a clear title and a description that summarizes the change and how it was tested. Reply with the PR URL when you are done."

// PRPrompt asks the agent to open a draft pull request against base.
func PRPrompt(base string) string { return fmt.Sprintf(prPrompt, base) }

// ReviewPrompt is the first instruction for a sandbox made to review a PR
// when no task is given.
const ReviewPrompt = "Review the pull request checked out on this branch. Summarize what it changes, run the relevant tests, and list any problems you find. Do not comment on the PR until I explicitly approve it."

func (s *Service) startSandbox(name string) Step {
	be := s.Backend
	return Step{Busy: "Starting the sandbox", Verb: "Starting", Run: func(ctx context.Context) (string, error) {
		return "", be.Start(ctx, name)
	}}
}

// StartAgent starts the agent in a running sandbox. It is a no-op when the
// agent's session is already up.
func (s *Service) StartAgent(rec *store.Sandbox, prompt string, resume bool) Step {
	be, name := s.Backend, rec.Name
	script := agent.StartScript(agent.StartOptions{Agent: rec.Agent, Prompt: prompt, Resume: resume, Model: SandboxModel(rec)})
	return Step{Busy: "Starting the agent", Verb: "Starting the agent in", Run: func(ctx context.Context) (string, error) {
		return be.Exec(ctx, name, script)
	}}
}

// Resume starts a stopped sandbox and continues its agent's conversation.
func (s *Service) Resume(rec *store.Sandbox) []Step {
	return []Step{s.startSandbox(rec.Name), s.StartAgent(rec, "", true)}
}

// Stop stops the sandbox. Its clone and branch are kept.
func (s *Service) Stop(name string) []Step {
	be := s.Backend
	return []Step{{Busy: "Stopping", Verb: "Stopping", Run: func(ctx context.Context) (string, error) {
		return "", be.Stop(ctx, name)
	}}}
}

// Remove deletes the sandbox and forgets it, stopping it first when it is
// running.
func (s *Service) Remove(name string, running bool) []Step {
	be, st := s.Backend, s.Store
	return []Step{{Busy: "Removing", Verb: "Removing", Run: func(ctx context.Context) (string, error) {
		if running {
			if err := be.Stop(ctx, name); err != nil {
				return "", err
			}
		}
		if err := be.Remove(ctx, name); err != nil {
			return "", err
		}
		warn(st.RemoveSandbox(name), "forget a removed sandbox", "sandbox", name)
		return "", nil
	}}}
}

// PublishPort forwards a host port to the sandbox. Without a host port, the
// sandbox's own port number is used on the host too.
func (s *Service) PublishPort(name string, p sbx.Port) []Step {
	if p.HostPort == 0 {
		p.HostPort = p.SandboxPort
	}
	be := s.Backend
	return []Step{{Busy: fmt.Sprintf("Publishing port %d", p.HostPort), Verb: "Publishing a port on", Run: func(ctx context.Context) (string, error) {
		return "", be.PublishPort(ctx, name, p)
	}}}
}

// UnpublishPort stops forwarding ports the sandbox has published.
func (s *Service) UnpublishPort(name string, ports ...sbx.Port) []Step {
	be := s.Backend
	var steps []Step
	for _, p := range ports {
		steps = append(steps, Step{Busy: fmt.Sprintf("Unpublishing port %d", p.HostPort), Verb: "Unpublishing a port on", Run: func(ctx context.Context) (string, error) {
			return "", be.UnpublishPort(ctx, name, p)
		}})
	}
	return steps
}

// MatchPorts finds the published ports with want's host port. Its other
// fields narrow the match when they are set.
func MatchPorts(published []sbx.Port, want sbx.Port) []sbx.Port {
	var out []sbx.Port
	for _, p := range published {
		if p.HostPort == want.HostPort && (want.SandboxPort == 0 || p.SandboxPort == want.SandboxPort) &&
			(want.Protocol == "" || p.Protocol == want.Protocol) && (want.HostIP == "" || p.HostIP == want.HostIP) {
			out = append(out, p)
		}
	}
	return out
}

// Send gives the agent text to act on. A stopped sandbox is started, and
// an agent that isn't running is started with the text as its first
// instruction.
func (s *Service) Send(rec *store.Sandbox, running, agentUp bool, text string) []Step {
	switch {
	case !running:
		return []Step{s.startSandbox(rec.Name), s.StartAgent(rec, text, true)}
	case !agentUp:
		return []Step{s.StartAgent(rec, text, true)}
	}
	be, name := s.Backend, rec.Name
	return []Step{{Busy: "Sending your message", Verb: "Messaging", Run: func(ctx context.Context) (string, error) {
		return be.Exec(ctx, name, agent.SendScript(text))
	}}}
}

// AttachDir is where attached files go in a sandbox. It is outside the
// workspace, so they never show up as changes or get committed.
const AttachDir = "/tmp/sbx-attachments"

// SendFiles copies files on the host into dir in the sandbox, then sends
// text as Send does. sbx cp needs the sandbox running, so a stopped one is
// started before the copy. The text should name the files' paths in the
// sandbox, as AttachNote does.
func (s *Service) SendFiles(rec *store.Sandbox, running, agentUp bool, text, dir string, files []string) []Step {
	steps := s.Send(rec, running, agentUp, text)
	if len(files) == 0 {
		return steps
	}
	attach := s.copyIn(rec.Name, dir, files)
	if !running {
		return append([]Step{steps[0], attach}, steps[1:]...)
	}
	return append([]Step{attach}, steps...)
}

// copyIn makes dir in the sandbox and copies each file into it under its
// own name. dir is made by the caller, so it needs no quoting beyond
// what's here.
func (s *Service) copyIn(name, dir string, files []string) Step {
	be := s.Backend
	busy := "Attaching a file"
	if len(files) > 1 {
		busy = fmt.Sprintf("Attaching %d files", len(files))
	}
	return Step{Busy: busy, Verb: "Attaching files to", Run: func(ctx context.Context) (string, error) {
		if _, err := be.Exec(ctx, name, fmt.Sprintf("mkdir -p '%s'", dir)); err != nil {
			return "", err
		}
		for _, f := range files {
			if err := be.CopyIn(ctx, name, f, path.Join(dir, filepath.Base(f))); err != nil {
				return "", err
			}
		}
		// The copies keep the Mac user's uid, so the agent can read them
		// but not change or delete them. Giving them to the sandbox's user
		// needs sudo, which a sandbox may not allow, so it is only tried.
		_, err := be.Exec(ctx, name, fmt.Sprintf(`sudo -n chown -R "$(id -u):$(id -g)" '%s' 2>/dev/null || true`, dir))
		return "", err
	}}
}

// AttachNote adds the paths of attached files to a message, on the same
// line so the agent's prompt doesn't take a newline as Enter. A message
// with no text is just the list.
func AttachNote(text string, paths []string) string {
	if len(paths) == 0 {
		return text
	}
	what := "Attached file: "
	if len(paths) > 1 {
		what = "Attached files: "
	}
	note := what + strings.Join(paths, ", ")
	if text == "" {
		return note
	}
	return text + " (" + note + ")"
}

// CheckoutLocal pulls a clone-mode sandbox's branch into the project's own
// checkout. Whenever such a sandbox is running, sandboxd configures a
// "sandbox-NAME" remote on the project repo that fetches straight from the
// sandbox's git daemon, so this is just
// `git fetch` followed by `git checkout -B`.
func (s *Service) CheckoutLocal(p store.Project, rec *store.Sandbox) []Step {
	branch, path := rec.Branch, p.Path
	remote := "sandbox-" + rec.Name
	return []Step{{Busy: "Checking out " + branch, Verb: "Checking out", Run: func(ctx context.Context) (string, error) {
		if _, err := gitRun(ctx, path, "fetch", remote); err != nil {
			return "", err
		}
		return gitRun(ctx, path, "checkout", "-B", branch, remote+"/"+branch)
	}}}
}

func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], lastLine(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrNeedsStart reports that reading something would mean starting the
// sandbox, which only the user should decide to do.
var ErrNeedsStart = errors.New("the sandbox has to be running")

// Diff returns a function that reads the patch behind the sandbox's
// changes. A running sandbox is asked; a stopped direct-mode sandbox works
// in the project's own checkout, so its diff is read there without
// starting it. A stopped private clone gives ErrNeedsStart.
func (s *Service) Diff(rec *store.Sandbox, p store.Project, live sbx.Sandbox, running bool) (func(ctx context.Context) (string, error), error) {
	script := agent.DiffScript(p.DefaultBranch)
	switch {
	case running:
		be, name := s.Backend, rec.Name
		return func(ctx context.Context) (string, error) { return be.Exec(ctx, name, script) }, nil
	case !rec.Clone && !s.Demo:
		dir := live.Workspace()
		if dir == "" {
			dir = p.Path
		}
		return func(ctx context.Context) (string, error) {
			c := exec.CommandContext(ctx, "bash", "-c", script)
			c.Dir = dir
			out, err := c.Output()
			return string(out), err
		}, nil
	}
	return nil, ErrNeedsStart
}
