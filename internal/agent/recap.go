package agent

import (
	"strconv"
	"strings"
	"time"
)

// Recap is the agent's whole reply at the end of its last turn, which is
// usually its own account of what it did and what it needs.
type Recap struct {
	When time.Time
	Text string
}

// parseRecap reads the @@recap probe section: a Unix time, then the reply.
func parseRecap(lines []string) *Recap {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) < 2 {
		return nil
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	if text == "" {
		return nil
	}
	return &Recap{When: time.Unix(ts, 0), Text: text}
}
