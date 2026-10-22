package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realProbe is trimmed output from a probe against Claude Code 2.1 running
// with the hooks from StartScript.
const realProbe = `@@agent
up
@@clone
yes
@@events
{"ts":1792161344,"ev":"SessionStart","reason":"startup"}
{"ts":1792161344,"ev":"UserPromptSubmit","prompt":"Use the Bash tool to run: echo hi"}
{"ts":1792161346,"ev":"PreToolUse","tool":"Bash","cmd":"echo hi","desc":"Echo a string"}
{"ts":1792161346,"ev":"PostToolUseFailure","tool":"Bash","cmd":"echo hi","msg":"Exit code 2\n/bin/bash: eval: line 1: unexpected EOF"}
not json
{"ts":1792161352,"ev":"Stop","msg":"DONE\n\nmore detail"}
@@branch
feat/hello
@@base
origin/main
@@commits
4a6cebf	1792161350	add b
@@diff
1	0	b.txt
-	-	logo.png
`

func TestParseProbe(t *testing.T) {
	p := ParseProbe(realProbe)
	if !p.AgentUp || !p.Clone {
		t.Fatalf("AgentUp=%v Clone=%v, want both true", p.AgentUp, p.Clone)
	}
	if len(p.Events) != 5 {
		t.Fatalf("got %d events, want 5 (malformed lines skipped)", len(p.Events))
	}
	if p.Branch != "feat/hello" || p.Base != "origin/main" {
		t.Fatalf("branch %q base %q", p.Branch, p.Base)
	}
	if len(p.Commits) != 1 || p.Commits[0].Subject != "add b" || p.Commits[0].Hash != "4a6cebf" {
		t.Fatalf("commits %+v", p.Commits)
	}
	if len(p.Files) != 2 || p.Files[0].Add != 1 || !p.Files[1].Binary {
		t.Fatalf("files %+v", p.Files)
	}
	if a, d := p.Totals(); a != 1 || d != 0 {
		t.Fatalf("totals %d %d", a, d)
	}
}

func probeWith(up bool, evs ...Event) *Probe {
	return &Probe{AgentUp: up, Events: evs}
}

func TestDerive(t *testing.T) {
	one := 1
	cases := []struct {
		name    string
		running bool
		probe   *Probe
		agent   string
		want    State
		text    string
	}{
		{"stopped", false, nil, "claude", StateStopped, "Stopped"},
		{"no probe yet", true, nil, "claude", StateStarting, "Checking"},
		{"agent crashed", true, probeWith(false, Event{Ev: "AgentExit", Code: &one}), "claude", StateFailed, "code 1"},
		{"agent not started", true, probeWith(false), "claude", StateIdle, "not running"},
		{"claude booting", true, probeWith(true), "claude", StateStarting, "Starting"},
		{"agent without hooks", true, probeWith(true), "gemini", StateWorking, "session running"},
		{"codex booting", true, probeWith(true), "codex", StateStarting, "Starting"},
		{"codex patch", true, probeWith(true, Event{Ev: "PreToolUse", Tool: "apply_patch", File: "/ws/a.go", Files: 3}), "codex", StateWorking, "Editing a.go and 2 more"},
		{"codex interrupted", true, probeWith(true, Event{Ev: "Interrupt"}), "codex", StateReady, "Interrupted"},
		{"permission request", true, probeWith(true, Event{Ev: "PermissionRequest", Tool: "Bash"}), "codex", StateNeedsYou, "permission to use Bash"},
		{"tool use", true, probeWith(true, Event{Ev: "PreToolUse", Tool: "Edit", File: "/ws/a/b.go"}), "claude", StateWorking, "Editing a/b.go"},
		{"bash with description", true, probeWith(true, Event{Ev: "PreToolUse", Tool: "Bash", Cmd: "go test ./...", Desc: "Run the tests"}), "claude", StateWorking, "Run the tests"},
		{"asks a question", true, probeWith(true, Event{Ev: "Notification", NType: "elicitation_dialog", Msg: "Retry or fail?"}), "claude", StateNeedsYou, "Retry or fail?"},
		{"idle prompt", true, probeWith(true, Event{Ev: "Stop"}, Event{Ev: "Notification", NType: "idle_prompt"}), "claude", StateReady, "Waiting for you"},
		{"turn done", true, probeWith(true, Event{Ev: "Stop", Msg: "## All green\nDetails"}), "claude", StateReady, "All green"},
		{"api failure", true, probeWith(true, Event{Ev: "StopFailure", Reason: "rate_limit"}), "claude", StateFailed, "rate limit"},
		{"resumed", true, probeWith(true, Event{Ev: "SessionStart", Reason: "resume"}), "claude", StateReady, "Resumed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Derive(c.running, c.probe, c.agent, "/ws")
			if got.State != c.want || !strings.Contains(got.Text, c.text) {
				t.Fatalf("Derive = %v %q, want %v containing %q", got.State, got.Text, c.want, c.text)
			}
		})
	}
}

func TestFeed(t *testing.T) {
	evs := []Event{
		{TS: 1, Ev: "UserPromptSubmit", Prompt: "Fix it"},
		{TS: 2, Ev: "PreToolUse", Tool: "Read", File: "/ws/a.go"},
		{TS: 3, Ev: "PreToolUse", Tool: "Read", File: "/ws/b.go"},
		{TS: 4, Ev: "PreToolUse", Tool: "Read", File: "/ws/c.go"},
		{TS: 5, Ev: "PreToolUse", Tool: "Edit", File: "/ws/pkg/c.go"},
		{TS: 6, Ev: "PreToolUse", Tool: "Bash", Cmd: "git commit -m \"$(cat <<'EOF'\nfix(mcp): tear down a zombie\n\nCo-Authored-By: x\nEOF\n)\""},
		{TS: 7, Ev: "PreToolUse", Tool: "Bash", Cmd: `git commit -am "feat: quick"`},
		{TS: 8, Ev: "Notification", NType: "idle_prompt"},
	}
	got := Feed(evs, "/ws")
	want := []string{"Committed feat: quick", "Committed fix(mcp): tear down a zombie", "Edited", "Read 3 files", "You: Fix it"}
	if len(got) != len(want) {
		t.Fatalf("got %d items %+v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i].Text != w {
			t.Errorf("item %d = %q, want %q", i, got[i].Text, w)
		}
	}
	if got[2].Path != "pkg/c.go" {
		t.Errorf("edit path = %q, want workspace-relative pkg/c.go", got[2].Path)
	}
	if got[0].Kind != FeedGood {
		t.Errorf("commit should be highlighted")
	}
}

func TestScriptsEmbedTextSafely(t *testing.T) {
	tricky := `it's "quoted" $(rm -rf /) ` + "`x`"
	for _, s := range []string{
		StartScript(StartOptions{Agent: "claude", Prompt: tricky}),
		SendScript(tricky),
		GitSetupScript("branch", tricky, "main"),
	} {
		if strings.Contains(s, tricky) || strings.Contains(s, "rm -rf") {
			t.Fatalf("user text must only appear base64-encoded:\n%s", s)
		}
	}
}

func TestStartScriptPassesSettingsOnlyToClaude(t *testing.T) {
	if !strings.Contains(StartScript(StartOptions{Agent: "claude"}), "--settings") {
		t.Fatal("claude launch must load the hook settings")
	}
	if strings.Contains(runScript("codex", false, ""), "--settings") {
		t.Fatal("codex does not understand --settings")
	}
	codex := runScript("codex", true, "")
	for _, want := range []string{"args+=(-c 'hooks={", "--dangerously-bypass-hook-trust", "resume --last"} {
		if !strings.Contains(codex, want) {
			t.Fatalf("codex launch is missing %q:\n%s", want, codex)
		}
	}
	if strings.Index(codex, "-c 'hooks=") > strings.Index(codex, "resume --last") {
		t.Fatal("a flag must come first or sbx's start-agent drops Codex's default flags")
	}
	if strings.Contains(runScript("claude", false, ""), "--model") {
		t.Fatal("no model set must leave the choice to Claude Code")
	}
	if !strings.Contains(StartScript(StartOptions{Agent: "claude", Model: "opus"}), "args+=(--model 'opus')") {
		t.Fatal("claude launch must pass the project's model")
	}
	if !strings.Contains(runScript("codex", false, "gpt-6.1-sol"), "args+=(--model 'gpt-6.1-sol')") {
		t.Fatal("codex launch must pass the project's model")
	}
	if strings.Contains(runScript("gemini", false, "gpt-6.1-sol"), "--model") {
		t.Fatal("models must not reach unsupported agents")
	}
	if !strings.Contains(runScript("claude", false, ""), "claude update") {
		t.Fatal("claude launch must update Claude Code so model aliases resolve to the latest models")
	}
	if !strings.Contains(runScript("codex", false, ""), "codex update") {
		t.Fatal("codex launch must update Codex so its default model is current")
	}
	if strings.Contains(runScript("codex", false, ""), "claude update") {
		t.Fatal("only claude launches update Claude Code")
	}
	if strings.Contains(runScript("gemini", false, ""), " update ") {
		t.Fatal("agents without an update step must not run one")
	}
	if !strings.Contains(runScript("claude", true, ""), "--continue") {
		t.Fatal("resuming claude should continue the last conversation")
	}
}

func TestAttachForcesUTF8AndTruecolor(t *testing.T) {
	argv := AttachArgv()
	script := strings.Join(argv, " ")
	// sbx exec passes no locale; without -u tmux draws Claude Code's UI as
	// "qqq" and "_".
	if !strings.Contains(script, "exec tmux -u ") {
		t.Fatalf("attach must force UTF-8: %v", argv)
	}
	if !strings.Contains(script, "-T RGB") || !strings.Contains(script, "COLORTERM") {
		t.Fatalf("attach must declare RGB when the host terminal is truecolor: %v", argv)
	}
	if !strings.Contains(runScript("claude", false, ""), "export COLORTERM=truecolor") {
		t.Fatal("agents must be told they can emit 24-bit colour")
	}
	if !strings.Contains(runScript("claude", false, ""), "CLAUDE_CODE_TMUX_TRUECOLOR=1") {
		t.Fatal("Claude Code clamps to 256 colours inside tmux without the opt-out")
	}
}

// TestHookScript runs the real hook against Claude Code and Codex payload
// shapes, because the normalisation between them lives in its jq program.
func TestHookScript(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".sbx-projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(hook, []byte(hookScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Codex's PostToolUse for a shell command has only the output; the exit
	// code comes from its session log.
	transcript := filepath.Join(home, "rollout.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"c9","status":"failed","exit_code":4}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payloads := []string{
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","description":"Run tests"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: /ws/a.go\n@@\n-x\n+y\n*** Add File: /ws/b.go\n+z\n*** End Patch"}}`,
		`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"true"},"tool_response":"Chunk ID: 1\nProcess exited with code 0\nOutput:\n"}`,
		`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test"},"tool_response":"Process exited with code 2\nOutput:\nFAIL"}`,
		`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"c9","transcript_path":"` + transcript + `","tool_input":{"command":"make"},"tool_response":""}`,
		`{"hook_event_name":"Stop","last_assistant_message":"All done"}`,
		`{"hook_event_name":"Interrupt","turn_id":"t1"}`,
	}
	for _, p := range payloads {
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Stdin = strings.NewReader(p)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v\n%s", err, out)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p := ParseProbe("@@events\n" + string(raw))
	var got []string
	for _, e := range p.Events {
		got = append(got, e.Ev)
	}
	want := "PreToolUse PreToolUse PostToolUseFailure PostToolUseFailure Stop Interrupt"
	if strings.Join(got, " ") != want {
		t.Fatalf("events = %v, want %s (successful PostToolUse dropped)\n%s", got, want, raw)
	}
	patch := p.Events[1]
	if patch.File != "/ws/a.go" || patch.Files != 2 || patch.Cmd != "" {
		t.Fatalf("apply_patch event = %+v, want first file, a count of 2 and no patch text", patch)
	}
	if fail := p.Events[2]; fail.Msg != "Exit code 2" || fail.Tool != "Bash" {
		t.Fatalf("failure event = %+v", fail)
	}
	if fromLog := p.Events[3]; fromLog.Msg != "Exit code 4" || fromLog.Cmd != "make" {
		t.Fatalf("exit code from the Codex session log = %+v", fromLog)
	}
	if p.Events[0].Desc != "Run tests" || p.Events[4].Msg != "All done" {
		t.Fatalf("claude-shaped fields lost: %+v", p.Events)
	}
}

func TestRunScriptRecordsTheAgentsExitCode(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".sbx-projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// A stand-in agent that fails, found on PATH because start-agent is absent.
	if err := os.WriteFile(filepath.Join(bin, "fakeagent"), []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(runScript("fakeagent", false, ""), "/usr/local/lib/sandbox/start-agent", filepath.Join(bin, "missing"))
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run script: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	p := ParseProbe("@@events\n" + string(raw))
	if len(p.Events) != 1 || p.Events[0].Code == nil || *p.Events[0].Code != 7 {
		t.Fatalf("AgentExit = %s, want code 7", raw)
	}
}

// TestProbeReadsCodexTurnEnd runs the real probe against a Codex session log
// shaped like one left by /review, which ends its turn without a Stop hook.
func TestProbeReadsCodexTurnEnd(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	home := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "16")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(day, "rollout-old.jsonl")
	if err := os.WriteFile(old, []byte(`{"timestamp":"2026-10-16T10:00:00.000Z","type":"event_msg","payload":{"type":"task_started"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	log := `{"timestamp":"2026-10-16T12:00:00.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"1"}}
{"timestamp":"2026-10-16T12:00:05.000Z","type":"response_item","payload":{"type":"message","content":[{"type":"output_text","text":"{\"type\":\"task_started\"}"}]}}
{"timestamp":"2026-10-16T12:01:30.123Z","type":"event_msg","payload":{"type":"exited_review_mode"}}
{"timestamp":"2026-10-16T12:01:30.456Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"1","last_agent_message":null}}
`
	if err := os.WriteFile(filepath.Join(day, "rollout-new.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", ProbeScript("main"))
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	p := ParseProbe(string(out))
	want := time.Date(2026, 10, 16, 12, 1, 30, 0, time.UTC).Unix()
	if p.Turn == nil || p.Turn.Ev != "task_complete" || p.Turn.TS != want {
		t.Fatalf("turn = %+v, want task_complete at %d\n%s", p.Turn, want, out)
	}
}

func TestCodexTurnEndClosesAHooklessTurn(t *testing.T) {
	probe := func(turn string, turnTS int64, evs ...Event) *Probe {
		raw := "@@agent\nup\n@@events\n"
		for _, e := range evs {
			b, _ := json.Marshal(e)
			raw += string(b) + "\n"
		}
		if turn != "" {
			raw += fmt.Sprintf("@@turn\n{\"ts\":%d,\"ev\":%q}\n", turnTS, turn)
		}
		p := ParseProbe(raw)
		return &p
	}
	review := []Event{{TS: 100, Ev: "UserPromptSubmit", Prompt: "/review"}, {TS: 110, Ev: "PreToolUse", Tool: "Bash", Cmd: "git diff"}}
	cases := []struct {
		name  string
		probe *Probe
		want  State
		stops int
	}{
		{"review finished", probe("task_complete", 120, review...), StateReady, 1},
		{"same second", probe("task_complete", 110, review...), StateReady, 1},
		{"stale completion", probe("task_complete", 90, review...), StateWorking, 0},
		{"still running", probe("task_started", 100, review...), StateWorking, 0},
		{"no session log", probe("", 0, review...), StateWorking, 0},
		{"stop hook already ran", probe("task_complete", 120, append(review, Event{TS: 120, Ev: "Stop", Msg: "Done"})...), StateReady, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Derive(true, c.probe, "codex", "/ws")
			if got.State != c.want {
				t.Fatalf("Derive = %v %q, want %v", got.State, got.Text, c.want)
			}
			stops := 0
			for _, e := range c.probe.Events {
				if e.Ev == "Stop" {
					stops++
				}
			}
			if stops != c.stops {
				t.Fatalf("got %d Stop events, want %d: %+v", stops, c.stops, c.probe.Events)
			}
		})
	}
}

func TestHookKeepsTheWholeReply(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".sbx-projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(hook, []byte(hookScript), 0o755); err != nil {
		t.Fatal(err)
	}
	reply := "Done.\n\n## What changed\n- " + strings.Repeat("long line ", 60)
	stop, _ := json.Marshal(map[string]any{"hook_event_name": "Stop", "last_assistant_message": reply})
	for _, p := range []string{
		string(stop),
	} {
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Stdin = strings.NewReader(p)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v\n%s", err, out)
		}
	}
	turn, _ := os.ReadFile(filepath.Join(dir, "last-turn.txt"))
	p := ParseProbe("@@recap\n" + string(turn) + "@@branch\nmain\n")
	if p.Recap == nil || p.Recap.Text != strings.TrimSpace(reply) {
		t.Fatalf("recap = %+v, want the whole reply with its blank lines", p.Recap)
	}
	if p.Branch != "main" {
		t.Fatalf("sections after the reply lost: branch %q", p.Branch)
	}
}

func TestProbeReadsTheLastReply(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".sbx-projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last-turn.txt"), []byte("1700000000\nAll done.\n\n- one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", ProbeScript("main"))
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	p := ParseProbe(string(out))
	if p.Recap == nil || p.Recap.Text != "All done.\n\n- one" || p.Recap.When.Unix() != 1700000000 {
		t.Fatalf("recap = %+v\n%s", p.Recap, out)
	}
}

func TestParseDiff(t *testing.T) {
	raw := `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@ package main
 package main
-var x = 1
+var x = 2
diff --git a/new file.txt b/new file.txt
new file mode 100644
--- /dev/null
+++ b/new file.txt
@@ -0,0 +1 @@
+hello
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/old.go b/moved.go
similarity index 100%
rename from old.go
rename to moved.go
diff --git a/logo.png b/logo.png
Binary files a/logo.png and b/logo.png differ
diff --git "a/tab\there" "b/tab\there"
--- "a/tab\there"
+++ "b/tab\there"
@@ -1 +1 @@
-a
+b
`
	got := ParseDiff(raw)
	want := []struct{ path, note string }{
		{"main.go", ""}, {"new file.txt", "new file"}, {"gone.txt", "deleted"},
		{"moved.go", "renamed from old.go"}, {"logo.png", "binary"}, {"tab\there", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Path != w.path || got[i].Note != w.note {
			t.Errorf("file %d = %q (%q), want %q (%q)", i, got[i].Path, got[i].Note, w.path, w.note)
		}
	}
	if l := got[0].Lines; len(l) != 4 || l[0] != "@@ -1,3 +1,3 @@ package main" || l[3] != "+var x = 2" {
		t.Errorf("main.go lines = %q", l)
	}
	if len(got[3].Lines) != 0 || len(got[4].Lines) != 0 {
		t.Error("a pure rename and a binary file have no hunks")
	}
}

func TestDiffScriptMatchesTheNumstat(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "feat")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644)
	git("commit", "-qam", "change a")
	// Uncommitted edits count too, as they do in the probe's numstat.
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644)
	git("add", "b.txt")

	c := exec.Command("bash", "-c", DiffScript("main"))
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	files := ParseDiff(string(out))
	if len(files) != 2 || files[0].Path != "a.txt" || files[1].Path != "b.txt" || files[1].Note != "new file" {
		t.Fatalf("files = %+v", files)
	}
	if strings.Join(files[0].Lines[1:], "|") != "-one|+two" {
		t.Errorf("a.txt lines = %q", files[0].Lines)
	}
}

func TestGitSetupScriptBranchesFromRemoteHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(wd string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wd, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	remote, host := filepath.Join(dir, "remote"), filepath.Join(dir, "host")
	git(dir, "init", "-q", remote)
	git(remote, "commit", "-q", "--allow-empty", "-m", "on main")
	git(remote, "checkout", "-q", "-b", "feat")
	git(remote, "commit", "-q", "--allow-empty", "-m", "on feat")
	git(remote, "checkout", "-q", "main")
	git(dir, "clone", "-q", remote, host)
	git(host, "checkout", "-q", "feat")
	// The host's origin/HEAD has gone stale and the project remembered it.
	git(host, "remote", "set-head", "origin", "feat")
	cmd := exec.Command("bash", "-c", GitSetupScript("branch", "fix/thing", "feat"))
	cmd.Dir = host
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup script: %v\n%s", err, out)
	}
	if got := git(host, "branch", "--show-current"); got != "fix/thing" {
		t.Fatalf("on %q, want fix/thing", got)
	}
	if got, want := git(host, "rev-parse", "HEAD"), git(remote, "rev-parse", "main"); got != want {
		t.Fatalf("new branch starts at %s, want main's %s", got, want)
	}
}

func TestGitSetupScriptWithoutBranchChecksOutRemoteHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(wd string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wd, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	remote, host := filepath.Join(dir, "remote"), filepath.Join(dir, "host")
	git(dir, "init", "-q", remote)
	git(remote, "commit", "-q", "--allow-empty", "-m", "on main")
	git(dir, "clone", "-q", remote, host)
	git(host, "checkout", "-q", "-b", "feat")
	git(host, "commit", "-q", "--allow-empty", "-m", "on feat")
	// main moves on after the host cloned it.
	git(remote, "commit", "-q", "--allow-empty", "-m", "newer main")
	cmd := exec.Command("bash", "-c", GitSetupScript("branch", "", "feat"))
	cmd.Dir = host
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup script: %v\n%s", err, out)
	}
	if got := git(host, "branch", "--show-current"); got != "main" {
		t.Fatalf("on %q, want main", got)
	}
	if got, want := git(host, "rev-parse", "HEAD"), git(remote, "rev-parse", "main"); got != want {
		t.Fatalf("main is at %s, want the remote's %s", got, want)
	}
}
