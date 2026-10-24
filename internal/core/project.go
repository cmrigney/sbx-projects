package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cmrigney/sbx-projects/internal/store"
)

// InspectRepo reads the project that the git repository holding path
// makes: its root, origin and default branch.
func InspectRepo(path string) (store.Project, error) {
	root, err := gitOut(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return store.Project{}, fmt.Errorf("%s is not inside a git repository", path)
	}
	p := store.Project{Name: filepath.Base(root), Path: root, DefaultBranch: "main"}
	if u, err := gitOut(root, "remote", "get-url", "origin"); err == nil {
		p.Remote = PrettyRemote(u)
	}
	if h, err := gitOut(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		p.DefaultBranch = strings.TrimPrefix(h, "origin/")
	}
	return p, nil
}

// PrettyRemote shortens a git URL to host/owner/repo.
func PrettyRemote(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "ssh://")
	u = strings.TrimPrefix(u, "git@")
	return strings.Replace(u, ":", "/", 1)
}

// FillRemotes records the origin of projects added before their remote
// was read, so pull requests aren't refused for want of one.
func (s *Service) FillRemotes(projects []store.Project) {
	if s.Demo {
		return
	}
	for _, p := range projects {
		if p.Remote != "" {
			continue
		}
		if u, err := gitOut(p.Path, "remote", "get-url", "origin"); err == nil {
			warn(s.Store.SetRemote(p.ID, PrettyRemote(u)), "save the remote", "project", p.Name)
		}
	}
}

// ProjectsDir is where new projects go: the folder most existing projects
// sit in, or ~/Projects when there are none.
func ProjectsDir(projects []store.Project) string {
	count := map[string]int{}
	best := ""
	for _, p := range projects {
		d := filepath.Dir(p.Path)
		count[d]++
		if best == "" || count[d] > count[best] {
			best = d
		}
	}
	if best != "" {
		return best
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, "Projects")
	}
	return "Projects"
}

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// CheckProjectName refuses names that aren't a single, plain folder name.
func CheckProjectName(name string) error {
	if !projectName.MatchString(name) || len(name) > 100 {
		return errors.New("a project name uses letters, digits, '.', '-' and '_', and starts with a letter or digit")
	}
	return nil
}

var scpURL = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^-]`)

// IsCloneURL says whether s is a git URL to clone rather than a path.
func IsCloneURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "ssh://") || scpURL.MatchString(s)
}

// RepoName is the folder a clone of u goes in.
func RepoName(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	u = strings.TrimSuffix(u, ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return u
}

// NewProjectRequest is a project to make from scratch or by cloning.
type NewProjectRequest struct {
	// Path is the project's folder. It may exist, if it is empty or holds
	// files that aren't in a repo yet.
	Path string
	// CloneURL, when set, is cloned into Path instead of starting a repo.
	CloneURL string
	// Publish creates a private GitHub repo for a new project with gh and
	// pushes to it.
	Publish bool
}

// NewProject returns the steps that make the project's folder and record
// it. The project is filled in by the time the steps finish. Publishing
// comes last, so a project that couldn't be published is still usable.
func (s *Service) NewProject(r NewProjectRequest) (*store.Project, []Step, error) {
	path, err := filepath.Abs(strings.TrimSpace(r.Path))
	if err != nil {
		return nil, nil, err
	}
	name := filepath.Base(path)
	if err := CheckProjectName(name); err != nil {
		return nil, nil, err
	}
	url := strings.TrimSpace(r.CloneURL)
	if url != "" && !IsCloneURL(url) {
		return nil, nil, fmt.Errorf("%s isn't a git URL", url)
	}
	if r.Publish && url != "" {
		return nil, nil, errors.New("a cloned project already has a remote to publish to")
	}
	if !s.Demo {
		if err := checkTarget(path, url != ""); err != nil {
			return nil, nil, err
		}
	}

	p := &store.Project{}
	demo, st := s.Demo, s.Store
	var steps []Step
	if url != "" {
		steps = append(steps, Step{Busy: "Cloning " + url, Verb: "Cloning", Run: func(ctx context.Context) (string, error) {
			if demo {
				return "", nil
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			return run(ctx, filepath.Dir(path), "git", "clone", "-q", "--", url, path)
		}})
	} else {
		steps = append(steps, Step{Busy: "Starting a git repo in " + name, Verb: "Starting a repo for", Run: func(ctx context.Context) (string, error) {
			if demo {
				return "", nil
			}
			return "", initRepo(ctx, path)
		}})
	}
	var added store.Project
	steps = append(steps, Step{Busy: "Adding " + name, Verb: "Adding", Run: func(ctx context.Context) (string, error) {
		np := store.Project{Name: name, Path: path, DefaultBranch: "main"}
		if !demo {
			var err error
			if np, err = InspectRepo(path); err != nil {
				return "", err
			}
		}
		var err error
		added, err = st.AddProject(np)
		return "", err
	}, Apply: func(string) { *p = added }})
	if r.Publish {
		steps = append(steps, Step{Busy: "Publishing " + name + " to GitHub", Verb: "Publishing", Run: func(ctx context.Context) (string, error) {
			if demo {
				return "", nil
			}
			if _, err := run(ctx, path, "gh", "repo", "create", name, "--private", "--source", path, "--remote", "origin", "--push"); err != nil {
				return "", err
			}
			u, err := gitOut(path, "remote", "get-url", "origin")
			if err != nil {
				return "", err
			}
			remote := PrettyRemote(u)
			if err := st.SetRemote(added.ID, remote); err != nil {
				return "", err
			}
			return remote, nil
		}, Apply: func(remote string) { p.Remote = remote }})
	}
	return p, steps, nil
}

// checkTarget refuses a folder that's already a repo, or that a clone
// can't go into.
func checkTarget(path string, clone bool) error {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is a file", path)
	}
	if _, err := gitOut(path, "rev-parse", "--show-toplevel"); err == nil {
		return fmt.Errorf("%s is already in a git repository; add it instead", path)
	}
	if clone {
		if ents, _ := os.ReadDir(path); len(ents) > 0 {
			return fmt.Errorf("%s already has files in it", path)
		}
	}
	return nil
}

// initRepo starts a repo on main in path, making the folder if needed, and
// commits what is there with a README. Clone-mode sandboxes need that
// commit: they branch from main. A folder it made is removed if this fails.
func initRepo(ctx context.Context, path string) (err error) {
	if _, serr := os.Stat(path); errors.Is(serr, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
		defer func() {
			if err != nil {
				os.RemoveAll(path)
			}
		}()
	}
	if _, err := gitRun(ctx, path, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	readme := filepath.Join(path, "README.md")
	if _, err := os.Stat(readme); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(readme, []byte("# "+filepath.Base(path)+"\n"), 0o644); err != nil {
			return err
		}
	}
	if _, err := gitRun(ctx, path, "add", "-A"); err != nil {
		return err
	}
	_, err = gitRun(ctx, path, "commit", "-q", "-m", "Initial commit")
	return err
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// run runs a command in dir, keeping git and gh from waiting on a prompt
// nobody can answer.
func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1")
	out, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %s", name, args[0], lastLine(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
