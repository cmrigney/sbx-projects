// Command sbx-projects is a terminal UI for running coding agents in sandboxes
// through the sbx CLI, organised by project.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cmrigney/sbx-projects/internal/applog"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
	"github.com/cmrigney/sbx-projects/internal/ui"
)

func main() {
	defPath, _ := store.DefaultPath()
	dbPath := flag.String("db", defPath, "path to the projects database")
	sbxBin := flag.String("sbx", "sbx", "sbx binary to run")
	demo := flag.Bool("demo", false, "run against built-in demo data instead of sbx")
	logPath := flag.String("log", os.Getenv("SBX_PROJECTS_LOG"), `debug log file; empty for a new one in the temp directory, "off" for none`)
	logLevel := slog.LevelInfo
	flag.TextVar(&logLevel, "log-level", logLevel, "debug log level: debug, info, warn or error")
	flag.Parse()

	path, closer, err := applog.Open(*logPath, logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbx-projects: no debug log:", err)
	}
	defer closer.Close()
	if path != "" {
		fmt.Fprintln(os.Stderr, "sbx-projects: debug log at", path)
	}
	slog.Info("starting", "args", os.Args[1:], "db", *dbPath, "sbx", *sbxBin, "demo", *demo, "os", runtime.GOOS, "arch", runtime.GOARCH)

	if err := run(*dbPath, *sbxBin, *demo, path); err != nil {
		slog.Error("exiting", "err", err)
		closer.Close()
		fmt.Fprintln(os.Stderr, "sbx-projects:", err)
		if path != "" {
			fmt.Fprintln(os.Stderr, "sbx-projects: details in", path)
		}
		os.Exit(1)
	}
	slog.Info("exiting")
}

func run(dbPath, sbxBin string, demo bool, logPath string) error {
	var be sbx.Backend
	if demo {
		dbPath = ":memory:"
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if demo {
		be = seedDemo(st)
	} else {
		cli := sbx.NewCLI(sbxBin)
		if err := cli.Check(); err != nil {
			return fmt.Errorf("%w; install the sbx CLI or pass --sbx", err)
		}
		be = cli
		ps, err := st.Projects()
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			if p := ui.DefaultProjectPath(); p != "" {
				addIfRepo(st, p)
			}
		}
	}

	start := ui.DefaultProjectPath()
	if demo {
		start = demoStartPath
	}
	var logs func() []applog.Entry
	if logPath != "" {
		logs = applog.Recent
	}
	m, err := ui.New(ui.Options{Backend: be, Store: st, Mock: demo, StartPath: start, HostCPUs: runtime.NumCPU(), HostMemMiB: hostMemMiB(), LogPath: logPath, Logs: logs})
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

// addIfRepo seeds the first project from the working directory so a fresh
// install opens on something useful.
func addIfRepo(st *store.Store, path string) {
	p, err := core.InspectRepo(path)
	if err != nil {
		return
	}
	if _, err := st.AddProject(p); err != nil {
		slog.Warn("add the first project", "path", p.Path, "err", err)
	}
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
