package core

import (
	"fmt"
	"regexp"
	"strings"
)

// SandboxName is the sbx name for a new sandbox. source is "branch", "pr"
// or "repo"; value is the branch name or the PR number or URL.
func SandboxName(agentName, project, source, value string) string {
	v := strings.TrimSpace(value)
	switch {
	case source == "repo":
		return agentName + "-" + Slug(project)
	case source == "pr":
		return agentName + "-pr-" + Digits(v)
	case v == "":
		return agentName + "-" + Slug(project)
	}
	return agentName + "-" + Slug(v)
}

// uniqueName numbers a name another sandbox already has: name-2, name-3...
func uniqueName(name string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", name, i); !taken(n) {
			return n
		}
	}
}

var slugRe = regexp.MustCompile(`[^a-z0-9.-]+`)

func Slug(v string) string {
	if i := strings.IndexByte(v, '/'); i >= 0 && i < len(v)-1 {
		v = v[i+1:]
	}
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(v), "-"), "-.")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-.")
	}
	if s == "" {
		return "work"
	}
	return s
}

// Digits is the PR number in "#4127", "4127" or a pull request URL.
func Digits(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			break
		}
	}
	if b.Len() == 0 {
		// URLs carry the number last.
		parts := strings.Split(strings.TrimRight(v, "/"), "/")
		for _, r := range parts[len(parts)-1] {
			if r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
	}
	if b.Len() == 0 {
		return "0"
	}
	return b.String()
}
