package agent

import (
	"bufio"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	TS   int64  `json:"ts"`
	Ev   string `json:"ev"`
	Tool string `json:"tool"`
	Cmd  string `json:"cmd"`
	Desc string `json:"desc"`
	File string `json:"file"`
	// Files counts the files in a multi-file Codex patch.
	Files   int    `json:"files"`
	Pattern string `json:"pattern"`
	Msg     string `json:"msg"`
	NType   string `json:"ntype"`
	Prompt  string `json:"prompt"`
	Reason  string `json:"reason"`
	Code    *int   `json:"code"`
}

func (e Event) Time() time.Time { return time.Unix(e.TS, 0) }

type Commit struct {
	Hash    string
	When    time.Time
	Subject string
}

type FileChange struct {
	Path     string
	Add, Del int
	Binary   bool
}

// Probe is one snapshot of a running sandbox.
type Probe struct {
	AgentUp bool
	Clone   bool
	Events  []Event
	// Turn is the last turn boundary in Codex's session log, if any.
	Turn    *Event
	Recap   *Recap
	Branch  string
	Base    string
	Commits []Commit
	Files   []FileChange
}

func ParseProbe(raw string) Probe {
	var p Probe
	section := ""
	var recap []string
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@@") {
			section = strings.TrimPrefix(line, "@@")
			continue
		}
		// The reply keeps its blank lines; they separate its paragraphs.
		if section == "recap" {
			recap = append(recap, line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch section {
		case "agent":
			p.AgentUp = strings.TrimSpace(line) == "up"
		case "clone":
			p.Clone = strings.TrimSpace(line) == "yes"
		case "events":
			var e Event
			if json.Unmarshal([]byte(line), &e) == nil && e.Ev != "" {
				p.Events = append(p.Events, e)
			}
		case "turn":
			var e Event
			if json.Unmarshal([]byte(line), &e) == nil && e.Ev != "" {
				p.Turn = &e
			}
		case "branch":
			p.Branch = strings.TrimSpace(line)
		case "base":
			p.Base = strings.TrimSpace(line)
		case "commits":
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 {
				ts, _ := strconv.ParseInt(parts[1], 10, 64)
				p.Commits = append(p.Commits, Commit{Hash: parts[0], When: time.Unix(ts, 0), Subject: parts[2]})
			}
		case "diff":
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 {
				fc := FileChange{Path: parts[2]}
				if parts[0] == "-" {
					fc.Binary = true
				} else {
					fc.Add, _ = strconv.Atoi(parts[0])
					fc.Del, _ = strconv.Atoi(parts[1])
				}
				p.Files = append(p.Files, fc)
			}
		}
	}
	p.Recap = parseRecap(recap)
	p.closeTurn()
	return p
}

// closeTurn adds the Stop that Codex leaves out when a turn ends without
// running its Stop hook, as after /review. The turn counts as finished
// when the session log's last boundary is a completion no older than the
// last hook event and the hooks still show the agent mid-turn.
func (p *Probe) closeTurn() {
	if p.Turn == nil || p.Turn.Ev != "task_complete" {
		return
	}
	last, ok := lastEvent(p.Events)
	if !ok || p.Turn.TS < last.TS {
		return
	}
	switch last.Ev {
	case "UserPromptSubmit", "PreToolUse", "PostToolUseFailure":
		p.Events = append(p.Events, Event{TS: p.Turn.TS, Ev: "Stop"})
	}
}

func (p Probe) Totals() (add, del int) {
	for _, f := range p.Files {
		add += f.Add
		del += f.Del
	}
	return add, del
}
