package agent

import (
	"strconv"
	"strings"
)

// FileDiff is one file's part of a unified diff.
type FileDiff struct {
	Path string
	// Note says what happened to a file that has no hunks to show, or
	// alongside them: "new file", "deleted", "renamed from X", "binary".
	Note string
	// Lines are the hunks, starting at the first "@@" line.
	Lines []string
}

// ParseDiff splits git diff output into files.
func ParseDiff(raw string) []FileDiff {
	var out []FileDiff
	var f *FileDiff
	inHunks := false
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			out = append(out, FileDiff{Path: headerPath(line)})
			f, inHunks = &out[len(out)-1], false
			continue
		}
		if f == nil {
			continue
		}
		if inHunks || strings.HasPrefix(line, "@@") {
			inHunks = true
			f.Lines = append(f.Lines, line)
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++ "):
			if p := diffPath(strings.TrimPrefix(line, "+++ "), "b/"); p != "" {
				f.Path = p
			}
		case strings.HasPrefix(line, "--- "):
			if f.Note == "deleted" {
				if p := diffPath(strings.TrimPrefix(line, "--- "), "a/"); p != "" {
					f.Path = p
				}
			}
		case strings.HasPrefix(line, "new file mode"):
			f.Note = "new file"
		case strings.HasPrefix(line, "deleted file mode"):
			f.Note = "deleted"
		case strings.HasPrefix(line, "rename from "):
			f.Note = "renamed from " + unquote(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			f.Path = unquote(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "Binary files "):
			f.Note = "binary"
		}
	}
	return out
}

// headerPath guesses the path from "diff --git a/X b/X", which is only
// ambiguous when the path contains " b/"; the "+++" line, when there is
// one, settles it.
func headerPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return unquote(rest[i+3:])
	}
	if i := strings.LastIndex(rest, ` "b/`); i >= 0 {
		return unquote(`"` + rest[i+4:])
	}
	return rest
}

func diffPath(s, prefix string) string {
	s = unquote(s)
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}

// unquote undoes git's C-style quoting of unusual paths.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}
