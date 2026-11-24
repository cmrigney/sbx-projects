package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
)

const (
	// maxCopied is what the Wio's clipboard holds, and maxCopiedJSON what
	// fits its message buffer once quotes and backslashes are escaped.
	maxCopied     = 1000
	maxCopiedJSON = 1200
	// maxPRLink is what the Wio's PR field holds.
	maxPRLink = 119
)

var prLink = regexp.MustCompile(`https://([^/\s]+)/([^/\s]+)/([^/\s]+)/pull/(\d+)`)

// plainReply makes a reply safe to type into a terminal: one line, so that
// pasting it can't press Enter, with no control characters. It returns as
// much as the Wio can hold, with its length and the whole reply's in runes.
func plainReply(md string) (text string, copied, total int) {
	flat := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(md, ""))
	flat = strings.Join(strings.Fields(flat), " ")
	total = utf8.RuneCountInString(flat)

	size, encoded := 0, 0
	for i, r := range flat {
		n := utf8.RuneLen(r)
		cost := n
		if r == '"' || r == '\\' {
			cost++
		}
		if size+n > maxCopied || encoded+cost > maxCopiedJSON {
			return flat[:i], copied, total
		}
		size += n
		encoded += cost
		copied++
	}
	return flat, copied, total
}

// lastPRLink is the last pull request link in text that's on the project's
// repository, without anything after its number.
func lastPRLink(text, remote string) string {
	matches := prLink.FindAllStringSubmatch(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		link := "https://" + m[1] + "/" + m[2] + "/" + m[3] + "/pull/" + m[4]
		if strings.EqualFold(m[1]+"/"+m[2]+"/"+m[3], remote) && len(link) <= maxPRLink {
			return link
		}
	}
	return ""
}

// replyAction answers the Wio's terminal menu on the terminal's socket, as
// the Wio has memory for only one TLS connection. Its errors are short enough
// for the status bar.
func (s *Server) replyAction(ctx context.Context, c *websocket.Conn, r *http.Request, kind string) {
	var answer any
	failed := func(message string) error {
		answer = map[string]string{"type": "reply-error", "message": message}
		return nil
	}
	err := s.withSandbox(r, func(it item) error {
		if it.probe == nil || it.probe.Recap == nil {
			if !agent.ReportsActivity(it.rec.Agent) {
				return failed(core.AgentLabel(it.rec.Agent) + " has no replies")
			}
			return failed("No reply saved yet")
		}
		reply := it.probe.Recap.Text
		if kind == "reply-copy" {
			text, copied, total := plainReply(reply)
			if text == "" {
				return failed("The reply is empty")
			}
			answer = map[string]any{"type": "reply", "text": text, "copied": copied, "total": total}
			return nil
		}
		link := lastPRLink(reply, it.project.Remote)
		if link == "" {
			return failed("No PR link in last reply")
		}
		answer = map[string]any{"type": "review", "url": link, "projectId": it.project.ID}
		return nil
	})
	if err != nil {
		answer = map[string]string{"type": "reply-error", "message": "Sandbox not found"}
	}
	writeTextJSON(ctx, c, answer)
}
