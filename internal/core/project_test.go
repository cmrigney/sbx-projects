package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/store"
)

// realGit gives a service that touches the disk, with git kept away from
// the user's config so commits neither sign nor ask who's committing.
func realGit(t *testing.T) *Service {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Test\n\temail = test@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	svc, _, _ := newTestService(t, nil)
	svc.Demo = false
	return svc
}

func TestNewProjectFromScratch(t *testing.T) {
	svc := realGit(t)
	path := filepath.Join(t.TempDir(), "nested", "fresh")
	p, steps, err := svc.NewProject(NewProjectRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 || p.Name != "fresh" || p.Path != path || p.DefaultBranch != "main" || p.Remote != "" {
		t.Fatalf("project = %+v", p)
	}
	if out, err := gitOut(path, "log", "--format=%s", "main"); err != nil || out != "Initial commit" {
		t.Fatalf("main should hold one first commit: %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Fatal(err)
	}

	// A clone-mode sandbox branches from main in a clone that has no
	// remote to fetch it from. Run its setup script in such a clone.
	clone := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", path, clone).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	for _, branch := range []string{"feat/first", ""} {
		c := exec.Command("bash", "-c", agent.GitSetupScript("branch", branch, p.DefaultBranch))
		c.Dir = clone
		out, err := c.Output()
		want := branch
		if want == "" {
			want = "main"
		}
		if err != nil || lastLine(string(out)) != want {
			t.Fatalf("setting up %q in the clone: %q, %v", branch, out, err)
		}
	}
}

func TestNewProjectAdoptsAFolderOfFiles(t *testing.T) {
	svc := realGit(t)
	path := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "todo.txt"), []byte("ship it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, steps, err := svc.NewProject(NewProjectRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if out, _ := gitOut(path, "ls-files"); out != "README.md\ntodo.txt" {
		t.Fatalf("the first commit should hold the folder's files: %q", out)
	}

	// Once it's a repo, it can't be started again.
	if _, _, err := svc.NewProject(NewProjectRequest{Path: path}); err == nil || !strings.Contains(err.Error(), "already in a git repository") {
		t.Fatalf("starting a repo twice: %v", err)
	}
}

func TestNewProjectRefusals(t *testing.T) {
	svc := realGit(t)
	dir := t.TempDir()
	full := filepath.Join(dir, "full")
	_ = os.MkdirAll(full, 0o755)
	_ = os.WriteFile(filepath.Join(full, "x"), nil, 0o644)
	for _, c := range []struct {
		r    NewProjectRequest
		want string
	}{
		{NewProjectRequest{Path: filepath.Join(dir, "-flag")}, "a project name"},
		{NewProjectRequest{Path: filepath.Join(dir, "bad name")}, "a project name"},
		{NewProjectRequest{Path: filepath.Join(dir, "a"), CloneURL: "/local/path"}, "isn't a git URL"},
		{NewProjectRequest{Path: filepath.Join(dir, "a"), CloneURL: "https://github.com/o/a", Publish: true}, "already has a remote"},
		{NewProjectRequest{Path: full, CloneURL: "https://github.com/o/full"}, "already has files"},
	} {
		if _, _, err := svc.NewProject(c.r); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want %q", c.r, err, c.want)
		}
	}
}

func TestNewProjectInDemoOnlyRecords(t *testing.T) {
	svc, _, _ := newTestService(t, nil)
	p, steps, err := svc.NewProject(NewProjectRequest{Path: "/src/demo-new", Publish: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo-new" || p.ID == 0 {
		t.Fatalf("project = %+v", p)
	}
	if _, err := os.Stat("/src/demo-new"); err == nil {
		t.Fatal("demo mode must not touch the disk")
	}
}

func TestCloneURLs(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/cmrigney/sbx-projects.git": "sbx-projects",
		"https://github.com/cmrigney/sbx-projects/":    "sbx-projects",
		"git@github.com:cmrigney/tools.git":            "tools",
		"ssh://git@example.com/a/b":                    "b",
	} {
		if !IsCloneURL(in) || RepoName(in) != want {
			t.Errorf("%q: IsCloneURL=%v RepoName=%q, want %q", in, IsCloneURL(in), RepoName(in), want)
		}
	}
	for _, in := range []string{"~/src/x", "/abs/path", "relative/dir", "cmrigney/sbx-projects"} {
		if IsCloneURL(in) {
			t.Errorf("%q is a path, not a URL", in)
		}
	}
}

func TestProjectsDir(t *testing.T) {
	h, _ := os.UserHomeDir()
	if got := ProjectsDir(); got != filepath.Join(h, "Projects") {
		t.Errorf("new projects go in ~/Projects: %q", got)
	}
}

func TestPRNeedsARemote(t *testing.T) {
	rec := &store.Sandbox{Clone: true}
	probe := &agent.Probe{Commits: []agent.Commit{{}}}
	if why := PRBlocker(store.Project{}, rec, probe); !strings.Contains(why, "no remote") {
		t.Errorf("a project without a remote can't take a PR: %q", why)
	}
	if why := PRBlocker(store.Project{Remote: "github.com/o/r"}, rec, probe); why != "" {
		t.Errorf("a project with a remote can: %q", why)
	}
}
