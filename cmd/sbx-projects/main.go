// Command sbx-projects is a terminal UI for running coding agents in sandboxes
// through the sbx CLI, organised by project.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
	"github.com/cmrigney/sbx-projects/internal/ui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "api" {
		if err := runAPICmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "sbx-projects api:", err)
			os.Exit(1)
		}
		return
	}
	defPath, _ := store.DefaultPath()
	dbPath := flag.String("db", defPath, "path to the projects database")
	sbxBin := flag.String("sbx", "sbx", "sbx binary to run")
	demo := flag.Bool("demo", false, "run against built-in demo data instead of sbx")
	flag.Parse()

	if err := run(*dbPath, *sbxBin, *demo); err != nil {
		fmt.Fprintln(os.Stderr, "sbx-projects:", err)
		os.Exit(1)
	}
}

func run(dbPath, sbxBin string, demo bool) error {
	st, be, err := openBackend(dbPath, sbxBin, demo)
	if err != nil {
		return err
	}
	defer st.Close()

	start := ui.DefaultProjectPath()
	if demo {
		start = demoStartPath
	}
	m, err := ui.New(ui.Options{Backend: be, Store: st, Mock: demo, StartPath: start, HostCPUs: runtime.NumCPU(), HostMemMiB: hostMemMiB()})
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

// openBackend opens the store and sbx, or demo data in memory.
func openBackend(dbPath, sbxBin string, demo bool) (*store.Store, sbx.Backend, error) {
	if demo {
		dbPath = ":memory:"
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	if demo {
		return st, seedDemo(st), nil
	}
	cli := sbx.NewCLI(sbxBin)
	if err := cli.Check(); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("%w; install the sbx CLI or pass --sbx", err)
	}
	ps, err := st.Projects()
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	if len(ps) == 0 {
		if p := ui.DefaultProjectPath(); p != "" {
			addIfRepo(st, p)
		}
	}
	return st, cli, nil
}

// addIfRepo seeds the first project from the working directory so a fresh
// install opens on something useful.
func addIfRepo(st *store.Store, path string) {
	out, err := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return
	}
	root := strings.TrimSpace(string(out))
	p := store.Project{Name: lastElem(root), Path: root}
	if h, err := exec.Command("git", "-C", root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		p.DefaultBranch = strings.TrimPrefix(strings.TrimSpace(string(h)), "origin/")
	}
	_, _ = st.AddProject(p)
}

func lastElem(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func hostMemMiB() int64 {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return n / (1 << 20)
	case "linux":
		b, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0
		}
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemTotal:" {
				kb, _ := strconv.ParseInt(f[1], 10, 64)
				return kb / 1024
			}
		}
	}
	return 0
}
