package applog

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestOpenWritesToPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	got, c, err := Open(path, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.DiscardHandler)) })
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	slog.Debug("hidden")
	slog.Error("boom", "err", "bad")
	c.Close()
	b, _ := os.ReadFile(path)
	if s := string(b); !strings.Contains(s, "boom") || strings.Contains(s, "hidden") {
		t.Fatalf("log = %q", s)
	}
}

func TestOpenOff(t *testing.T) {
	got, c, err := Open("OFF", slog.LevelInfo)
	if err != nil || got != "" || c == nil {
		t.Fatalf("Open(off) = %q, %v, %v", got, c, err)
	}
}

func TestOpenDefaultUsesTempDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := Dir()
	os.MkdirAll(dir, 0o700)
	stale := filepath.Join(dir, "sbx-projects-old.log")
	os.WriteFile(stale, nil, 0o600)
	old := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(stale, old, old)

	got, c, err := Open("", slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.DiscardHandler)) })
	if filepath.Dir(got) != dir {
		t.Fatalf("path = %q, want it in %q", got, dir)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale log kept: %v", err)
	}
}

func TestTail(t *testing.T) {
	if got := Tail("  abcdef \n", 3); got != "…def" {
		t.Fatalf("Tail = %q", got)
	}
	if got := Tail("ab", 3); got != "ab" {
		t.Fatalf("Tail = %q", got)
	}
}

func TestRecentKeepsTheLatestEntries(t *testing.T) {
	_, c, err := Open(filepath.Join(t.TempDir(), "x.log"), slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.DiscardHandler)) })

	slog.Debug("below the level")
	slog.With("sandbox", "box").WithGroup("g").Error("boom", "err", errors.New("bad"), "args", []string{"exec", "-i"})
	for i := range keepRecent {
		slog.Info("filler", "i", i)
	}
	got := Recent()
	if len(got) != keepRecent || got[len(got)-1].Attrs[0].Value != fmt.Sprint(keepRecent-1) {
		t.Fatalf("kept %d entries, last %+v", len(got), got[len(got)-1])
	}

	recent.entries = nil
	slog.With("sandbox", "box").WithGroup("g").Error("boom", "err", errors.New("bad"), "args", []string{"exec", "-i"})
	got = Recent()
	want := []Attr{{"sandbox", "box"}, {"g.err", "bad"}, {"g.args", "[exec -i]"}}
	if len(got) != 1 || got[0].Msg != "boom" || got[0].Level != slog.LevelError || !slices.Equal(got[0].Attrs, want) {
		t.Fatalf("Recent = %+v", got)
	}
}
