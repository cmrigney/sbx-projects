// Package applog keeps the debug log: what sbx-projects ran and what went
// wrong, in a file the user can read after the fact. The terminal belongs
// to the UI, so nothing is logged to it.
package applog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Off, given as the path, turns logging off.
const Off = "off"

// keepFor is how long old logs in the default directory are kept.
const keepFor = 7 * 24 * time.Hour

// Dir is where logs go when no path is given.
func Dir() string { return filepath.Join(os.TempDir(), "sbx-projects") }

// Open makes a log at path the default slog logger and returns the path.
// Entries also go to Recent.
// An empty path picks a new file in Dir for this run and drops logs there
// older than a week. With Off, or when the file can't be opened, logs are
// discarded and the path is empty. The closer is never nil.
func Open(path string, level slog.Level) (string, io.Closer, error) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	none := io.NopCloser(nil)
	if strings.EqualFold(path, Off) {
		return "", none, nil
	}
	if path == "" {
		dir := Dir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", none, fmt.Errorf("open the log: %w", err)
		}
		prune(dir, time.Now().Add(-keepFor))
		path = filepath.Join(dir, fmt.Sprintf("sbx-projects-%s-%d.log", time.Now().Format("20060102-150405"), os.Getpid()))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", none, fmt.Errorf("open the log: %w", err)
	}
	slog.SetDefault(slog.New(&keeper{next: slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})}))
	return path, f, nil
}

func prune(dir string, before time.Time) {
	old, _ := filepath.Glob(filepath.Join(dir, "sbx-projects-*.log"))
	for _, p := range old {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().Before(before) {
			_ = os.Remove(p)
		}
	}
}

// Tail keeps the end of long command output, which is where errors are.
func Tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
