package agent

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

type State int

const (
	StateStopped State = iota
	StateStarting
	StateWorking
	StateNeedsYou
	StateFailed
	StateReady
	StateIdle
)

type Status struct {
	State State
	Text  string
}

// Derive turns a probe into the one-line status shown for a sandbox.
// Agents without hooks only report whether their session is up.
func Derive(running bool, p *Probe, agentName, workspace string) Status {
	if !running {
		return Status{StateStopped, "Stopped, kept for later"}
	}
	if p == nil {
		return Status{StateStarting, "Checking on the agent"}
	}
	last, ok := lastEvent(p.Events)
	if !p.AgentUp {
		if ok && last.Ev == "AgentExit" && last.Code != nil && *last.Code != 0 {
			return Status{StateFailed, fmt.Sprintf("Agent exited with code %d", *last.Code)}
		}
		return Status{StateIdle, "Agent not running. Press enter to start it"}
	}
	if !ok || last.Ev == "AgentExit" {
		if ReportsActivity(agentName) {
			return Status{StateStarting, "Starting the agent"}
		}
		return Status{StateWorking, "Agent session running"}
	}
	switch last.Ev {
	case "SessionStart":
		if last.Reason == "resume" {
			return Status{StateReady, "Resumed, waiting for you"}
		}
		return Status{StateReady, "Waiting for your first instruction"}
	case "UserPromptSubmit":
		return Status{StateWorking, "Thinking"}
	case "PreToolUse":
		return Status{StateWorking, toolDoing(last, workspace)}
	case "PostToolUseFailure":
		return Status{StateWorking, fmt.Sprintf("%s failed, working on it", toolLabel(last.Tool))}
	case "Notification":
		switch last.NType {
		case "idle_prompt":
			return Status{StateReady, "Waiting for you"}
		case "auth_success":
			return Status{StateWorking, "Working"}
		}
		msg := firstLine(last.Msg)
		if msg == "" {
			msg = "Needs your input"
		}
		return Status{StateNeedsYou, msg}
	case "Stop":
		if s := firstLine(last.Msg); s != "" {
			return Status{StateReady, s}
		}
		return Status{StateReady, "Finished its turn, waiting for you"}
	case "StopFailure":
		r := last.Reason
		if r == "" {
			r = "an API error"
		}
		return Status{StateFailed, "Stopped by " + strings.ReplaceAll(r, "_", " ")}
	case "Interrupt":
		return Status{StateReady, "Interrupted, waiting for you"}
	case "PermissionRequest":
		return Status{StateNeedsYou, "Wants permission to use " + toolLabel(last.Tool)}
	case "SessionEnd":
		return Status{StateIdle, "Session ended"}
	}
	return Status{StateWorking, "Working"}
}

func lastEvent(evs []Event) (Event, bool) {
	if len(evs) == 0 {
		return Event{}, false
	}
	return evs[len(evs)-1], true
}

type FeedKind int

const (
	FeedPlain FeedKind = iota
	FeedYou
	FeedGood
	FeedWarn
	FeedBad
)

type FeedItem struct {
	When time.Time
	Text string
	// Path, when set, follows Text and may be shortened from the left.
	Path string
	Kind FeedKind
}

// Feed renders events newest first, folding runs of reads and searches so
// the feed reads like a summary rather than a log.
func Feed(evs []Event, workspace string) []FeedItem {
	var out []FeedItem
	var foldKey string
	var foldN int
	for _, e := range evs {
		item, key, ok := feedItem(e, workspace)
		if !ok {
			continue
		}
		if key != "" && key == foldKey && len(out) > 0 {
			foldN++
			out[len(out)-1] = FeedItem{When: e.Time(), Text: foldText(key, foldN), Kind: FeedPlain}
			continue
		}
		foldKey, foldN = key, 1
		out = append(out, item)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func foldText(key string, n int) string {
	switch key {
	case "read":
		return fmt.Sprintf("Read %d files", n)
	case "search":
		return fmt.Sprintf("Searched the code %d times", n)
	case "web":
		return fmt.Sprintf("Looked up %d pages", n)
	}
	return key
}

func feedItem(e Event, ws string) (FeedItem, string, bool) {
	it := FeedItem{When: e.Time()}
	switch e.Ev {
	case "SessionStart":
		if e.Reason == "resume" {
			it.Text = "Resumed the session"
		} else {
			it.Text = "Session started"
		}
	case "UserPromptSubmit":
		it.Text, it.Kind = "You: "+clip(firstLine(e.Prompt), 120), FeedYou
	case "PreToolUse":
		text, key := toolDone(e, ws)
		it.Text = text
		if p := toolPath(e, ws); p != "" {
			it.Text, it.Path = strings.TrimSuffix(text, " "+p), p
		}
		if strings.HasPrefix(text, "Committed") || strings.HasPrefix(text, "Opened") || strings.HasPrefix(text, "Pushed") {
			it.Kind = FeedGood
		}
		return it, key, true
	case "PostToolUseFailure":
		it.Text, it.Kind = toolLabel(e.Tool)+" failed: "+firstLine(e.Msg), FeedBad
	case "Notification":
		if e.NType == "idle_prompt" || e.NType == "auth_success" {
			return it, "", false
		}
		it.Text, it.Kind = "Asked: "+firstLine(e.Msg), FeedWarn
	case "Stop":
		it.Text = "Finished its turn"
		if s := firstLine(e.Msg); s != "" {
			it.Text += ": " + s
		}
	case "StopFailure":
		it.Text, it.Kind = "Turn failed: "+strings.ReplaceAll(e.Reason, "_", " "), FeedBad
	case "Interrupt":
		it.Text = "You interrupted it"
	case "PermissionRequest":
		it.Text, it.Kind = "Asked to use "+toolLabel(e.Tool), FeedWarn
	case "SessionEnd":
		it.Text = "Session ended"
	case "AgentExit":
		if e.Code != nil && *e.Code != 0 {
			it.Text, it.Kind = fmt.Sprintf("Agent exited with code %d", *e.Code), FeedBad
		} else {
			it.Text = "Agent exited"
		}
	default:
		return it, "", false
	}
	return it, "", true
}

func toolDoing(e Event, ws string) string {
	switch e.Tool {
	case "Bash":
		if e.Desc != "" {
			return e.Desc
		}
		return "Running " + firstLine(e.Cmd)
	case "Edit", "MultiEdit", "Write", "NotebookEdit", "apply_patch":
		return "Editing " + editTarget(e, ws)
	case "Read":
		return "Reading " + rel(e.File, ws)
	case "Grep", "Glob":
		return "Searching the code"
	case "Task", "Agent", "spawn_agent":
		if e.Desc != "" {
			return "Subagent: " + e.Desc
		}
		return "Running a subagent"
	case "WebFetch", "WebSearch":
		return "Looking things up"
	case "TodoWrite":
		return "Updating its plan"
	}
	return "Using " + toolLabel(e.Tool)
}

var (
	commitMsgRe  = regexp.MustCompile(`\s-[a-zA-Z]*m\s*(?:"([^"$]*)"|'([^']*)'|(\S+))`)
	heredocMsgRe = regexp.MustCompile(`<<-?\s*'?"?EOF'?"?\s*\n\s*([^\n]+)`)
)

func toolDone(e Event, ws string) (string, string) {
	switch e.Tool {
	case "Bash":
		cmd := strings.TrimSpace(e.Cmd)
		if strings.Contains(cmd, "git commit") {
			if m := heredocMsgRe.FindStringSubmatch(cmd); m != nil {
				return "Committed " + strings.TrimSpace(m[1]), ""
			}
			if m := commitMsgRe.FindStringSubmatch(cmd); m != nil {
				return "Committed " + m[1] + m[2] + m[3], ""
			}
			return "Committed changes", ""
		}
		if strings.Contains(cmd, "gh pr create") {
			return "Opened a pull request", ""
		}
		if strings.HasPrefix(cmd, "git push") {
			return "Pushed the branch", ""
		}
		return "Ran " + firstLine(cmd), ""
	case "Edit", "MultiEdit", "Write", "NotebookEdit", "apply_patch":
		return "Edited " + editTarget(e, ws), ""
	case "Read":
		return "Read " + rel(e.File, ws), "read"
	case "Grep", "Glob":
		if e.Pattern != "" {
			return "Searched for " + e.Pattern, "search"
		}
		return "Searched the code", "search"
	case "WebFetch", "WebSearch":
		return "Looked something up", "web"
	case "Task", "Agent", "spawn_agent":
		if e.Desc != "" {
			return "Started a subagent: " + e.Desc, ""
		}
		return "Started a subagent", ""
	case "TodoWrite":
		return "Updated its plan", ""
	}
	return "Used " + toolLabel(e.Tool), ""
}

func toolPath(e Event, ws string) string {
	switch e.Tool {
	case "Edit", "MultiEdit", "Write", "NotebookEdit", "Read", "apply_patch":
		return editTarget(e, ws)
	}
	return ""
}

func editTarget(e Event, ws string) string {
	t := rel(e.File, ws)
	if e.Files > 1 {
		t += fmt.Sprintf(" and %d more", e.Files-1)
	}
	return t
}

func toolLabel(t string) string {
	if strings.HasPrefix(t, "mcp__") {
		parts := strings.Split(t, "__")
		if len(parts) >= 3 {
			return parts[2] + " (" + parts[1] + ")"
		}
	}
	if t == "" {
		return "a tool"
	}
	return t
}

func rel(p, ws string) string {
	if p == "" {
		return "a file"
	}
	if ws != "" {
		ws = strings.TrimSuffix(ws, "/") + "/"
		if strings.HasPrefix(p, ws) {
			return strings.TrimPrefix(p, ws)
		}
	}
	if strings.HasPrefix(p, "/") {
		return path.Base(p)
	}
	return p
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.TrimLeft(s, "#*- ")
}
