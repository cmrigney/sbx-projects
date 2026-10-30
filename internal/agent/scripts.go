// Package agent runs coding agents headless inside sandboxes and reads back
// what they are doing.
//
// The agent runs in a tmux session on a private socket so it keeps working
// with nothing attached and can be attached to later. Claude Code and Codex
// report through hooks that append one JSON line per event to events.jsonl.
package agent

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	dir        = `$HOME/.sbx-projects`
	tmuxSocket = "sbxp"
	session    = "agent"
)

// AttachArgv attaches to the agent's tmux session.
//
// sbx exec sets no locale and forces TERM=xterm-256color, so tmux would
// otherwise treat the terminal as non-UTF-8 (drawing Claude Code's UI as
// "qqq" and "_") and squash 24-bit colour to 256. -u forces UTF-8; -T RGB is
// added only when the host terminal advertises truecolor through the
// COLORTERM that sbx forwards.
func AttachArgv() []string {
	return []string{"sh", "-c", `case "$COLORTERM" in truecolor|24bit) set -- -T RGB ;; *) set -- ;; esac
exec tmux -u "$@" -L ` + tmuxSocket + ` attach-session -t ` + session}
}

func ShellArgv() []string { return []string{"bash", "-l"} }

// hookScript is shared by every agent with hooks. It normalises the
// differences between them: Codex edits through apply_patch (the paths are
// inside the patch), and its PostToolUse for a shell command carries only the
// command's output. The exit code is looked up in the session log Codex
// names in transcript_path; only failing PostToolUse events are kept,
// rewritten as PostToolUseFailure to match Claude Code.
//
// The agent's reply at the end of each turn is also kept whole, in
// last-turn.txt, for reading without attaching.
//
// A new session (a fresh start or /clear, not a resume or compaction)
// clears both, so the activity shown is only the current conversation's.
const hookScript = `#!/bin/bash
D="$HOME/.sbx-projects"
f="$D/events.jsonl"
in=$(cat)
code=""
kind=$(printf '%s' "$in" | jq -r '.hook_event_name + ":" + (.tool_name // "")' 2>/dev/null)
case "$kind" in
  SessionStart:*)
    src=$(printf '%s' "$in" | jq -r '.source // empty' 2>/dev/null)
    case "$src" in
      startup|clear) : > "$f"; rm -f "$D/last-turn.txt" ;;
    esac ;;
  Stop:*)
    msg=$(printf '%s' "$in" | jq -r '.last_assistant_message // empty' 2>/dev/null)
    if [ -n "$msg" ]; then
      { date +%s; printf '%s\n' "$msg" | head -c 12000; } > "$D/last-turn.tmp" && mv "$D/last-turn.tmp" "$D/last-turn.txt"
    fi ;;
esac
if [ "$kind" = "PostToolUse:Bash" ]; then
  tp=$(printf '%s' "$in" | jq -r '.transcript_path // empty')
  id=$(printf '%s' "$in" | jq -r '.tool_use_id // empty')
  if [ -f "$tp" ] && [ -n "$id" ]; then
    code=$(tail -n 400 "$tp" | jq -r --arg id "$id" 'select(.payload.type == "item_completed" and .payload.item.id == $id) | .payload.item.exit_code // empty' 2>/dev/null | tail -n 1)
  fi
fi
printf '%s' "$in" | jq -c --argjson ts "$(date +%s)" --arg code "$code" '
  def cut(n): if . == null then null else (tostring | .[0:n]) end;
  def text: if type == "array" then join(" ") else tostring end;
  def patch_files: [(.tool_input.command // "" | tostring) | scan("\\*\\*\\* (?:Add|Update|Delete) File: ([^\\n]+)") | .[0]];
  def exit_code:
    if $code != "" then ($code | tonumber)
    else [(.tool_response // "" | tostring) | scan("(?:Process exited with code|Exit code:) (-?[0-9]+)") | .[0] | tonumber] | last end;
  if .hook_event_name == "PostToolUse" then
    exit_code as $c
    | if $c != null and $c != 0 then .hook_event_name = "PostToolUseFailure" | .error = "Exit code \($c)" else empty end
  else . end
  | (if .tool_name == "apply_patch" then patch_files else [] end) as $pf
  | {ts: $ts,
     ev: .hook_event_name,
     tool: .tool_name,
     cmd: (if .tool_name == "apply_patch" then null else (.tool_input.command | if . == null then null else text end | cut(300)) end),
     desc: (.tool_input.description | cut(160)),
     file: (.tool_input.file_path // .tool_input.notebook_path // .tool_input.path // $pf[0]),
     files: (if ($pf | length) > 1 then ($pf | length) else null end),
     pattern: (.tool_input.pattern | cut(120)),
     msg: ((.message // .last_assistant_message // .error) | cut(300)),
     ntype: .notification_type,
     prompt: ((.prompt // .input) | cut(300)),
     reason: (.reason // .source // .error_type)}
  | with_entries(select(.value != null))' >> "$f" 2>/dev/null
if [ "$(wc -l < "$f")" -gt 4000 ]; then tail -n 1000 "$f" > "$f.tmp" && mv "$f.tmp" "$f"; fi
exit 0
`

var hookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUseFailure", "Notification", "Stop", "StopFailure", "SessionEnd"}

// codexHookEvents are the Codex events that matter here. Codex has no
// Notification or PostToolUseFailure; the hook script derives failures from
// PostToolUse.
var codexHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "Interrupt", "SessionEnd"}

// ReportsActivity says whether an agent writes hook events, as opposed to
// only showing whether its session is up.
func ReportsActivity(agentName string) bool {
	return agentName == "claude" || agentName == "codex"
}

// codexHooksArgs builds bash that appends the Codex hook config to args.
// sbx rewrites ~/.codex/config.toml on every create, so hooks are passed as
// a -c override. Hooks from overrides are untrusted, so they only run with
// --dangerously-bypass-hook-trust, which older Codex releases don't have.
// The -c always goes first: sbx's start-agent only keeps Codex's default
// flags when the first argument is a flag.
func codexHooksArgs() string {
	var groups []string
	for _, ev := range codexHookEvents {
		groups = append(groups, ev+`=[{matcher="*",hooks=['"$H"']}]`)
	}
	// Codex caps some hook timeouts at 3s and warns about anything longer;
	// the hook takes milliseconds.
	return `H='{type="command",command="'"$D"'/hook.sh",timeout=3}'
args+=(-c 'hooks={` + strings.Join(groups, ",") + `}')
if codex --help 2>/dev/null | grep -q -- --dangerously-bypass-hook-trust; then args+=(--dangerously-bypass-hook-trust); fi
` + codexTrustArgs
}

// codexTrustArgs pre-trusts the workspace, as sbx does for Claude Code;
// otherwise a headless Codex waits at its trust prompt and never starts.
// The whole projects table is set because -c splits keys on every ".",
// which workspace paths often contain.
const codexTrustArgs = `tq() { printf '"%s"' "$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g')"; }
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
trust="$(tq "$PWD")={trust_level=\"trusted\"}"
[ "$root" != "$PWD" ] && trust="$trust,$(tq "$root")={trust_level=\"trusted\"}"
args+=(-c "projects={$trust}")
`

// updateScript updates an agent before it launches. Model aliases and the
// default model are resolved by the installed agent, and a sandbox starts
// on the release baked into its image; Claude Code's auto-update only takes
// effect on the next launch, so a fresh sandbox would get an older model.
//
// The update only runs when the agent lists the command: a Codex without
// it would take "update" as a prompt. It is bounded so a slow or blocked
// network delays the agent rather than keeping it from starting.
func updateScript(agentName, label string) string {
	return fmt.Sprintf(`if %[1]s --help 2>/dev/null | grep -qE '^ +update( |\||$)'; then
  echo "Updating %[2]s..."
  timeout 90 %[1]s update </dev/null >"$D/%[1]s-update.log" 2>&1 || true
fi
`, agentName, label)
}

func claudeSettings() string {
	var b strings.Builder
	b.WriteString(`{"hooks":{`)
	for i, ev := range hookEvents {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"%s":[{"matcher":"*","hooks":[{"type":"command","command":"$HOME/.sbx-projects/hook.sh","timeout":10}]}]`, ev)
	}
	b.WriteString("}}\n")
	return b.String()
}

const tmuxConf = `set -g status-style "bg=colour236,fg=colour250"
set -g status-left " sbx-projects "
set -g status-left-length 20
set -g status-right " ctrl-b d to return "
set -g window-status-format ""
set -g window-status-current-format ""
set -g mouse on
set -g history-limit 50000
set -g default-terminal "tmux-256color"
set -g escape-time 10
set -g focus-events on
`

// StartOptions configure a fresh agent launch.
type StartOptions struct {
	Agent  string
	Prompt string
	// Resume continues the previous conversation when the agent supports it.
	Resume bool
	// Model is passed to Claude Code or Codex as --model.
	Model string
}

// StartScript installs tmux if needed, writes the hook files and starts the
// agent in a detached tmux session. It is a no-op when the session exists.
func StartScript(o StartOptions) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	fmt.Fprintf(&b, "D=%s\nmkdir -p \"$D\"\n", dir)
	// A freshly created sandbox can still hold the dpkg lock or lack network
	// for a few seconds, so wait on the lock and retry before giving up.
	b.WriteString(`if ! command -v tmux >/dev/null 2>&1; then
  echo "installing tmux" >&2
  aptq() { sudo DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=60 -y -qq "$@"; }
  for i in 1 2 3 4 5; do
    if [ "$i" -gt 1 ]; then aptq update >"$D/apt.log" 2>&1 || true; fi
    aptq install tmux >"$D/apt.log" 2>&1 && break
    sleep $((i * 2))
  done
fi
if ! command -v tmux >/dev/null 2>&1; then
  err=$(grep -E '^(E|W):' "$D/apt.log" 2>/dev/null | tail -n 1)
  echo "tmux is not installed and apt-get could not install it${err:+: $err}" >&2
  exit 3
fi
`)
	heredoc(&b, "$D/hook.sh", hookScript)
	b.WriteString("chmod +x \"$D/hook.sh\"\n")
	heredoc(&b, "$D/claude-settings.json", claudeSettings())
	heredoc(&b, "$D/tmux.conf", tmuxConf)
	heredoc(&b, "$D/run.sh", runScript(o.Agent, o.Resume, o.Model))
	b.WriteString("chmod +x \"$D/run.sh\"\n")
	fmt.Fprintf(&b, "if tmux -L %s has-session -t %s 2>/dev/null; then echo already-running; exit 0; fi\n", tmuxSocket, session)
	if o.Prompt != "" {
		fmt.Fprintf(&b, "printf '%%s' '%s' | base64 -d > \"$D/prompt\"\n", base64.StdEncoding.EncodeToString([]byte(o.Prompt)))
	} else {
		b.WriteString("rm -f \"$D/prompt\"\n")
	}
	fmt.Fprintf(&b, "tmux -L %s -f \"$D/tmux.conf\" new-session -d -s %s -x 200 -y 50 -c \"$PWD\" \"$D/run.sh\"\n", tmuxSocket, session)
	b.WriteString("echo started\n")
	return b.String()
}

// runScript launches the agent through sbx's own start-agent launcher so the
// agent gets the same environment and default flags as "sbx run".
func runScript(agentName string, resume bool, model string) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\nD=\"$HOME/.sbx-projects\"\nargs=()\n")
	// The tmux server starts from a non-interactive exec with no COLORTERM,
	// which would keep agents at 256 colours. tmux converts colours down
	// for any client that can't show 24-bit.
	b.WriteString("export COLORTERM=truecolor\n")
	prompt := `p=""
if [ -s "$D/prompt" ]; then p=$(cat "$D/prompt"); rm -f "$D/prompt"; fi
`
	b.WriteString(prompt)
	switch agentName {
	case "claude":
		// Claude Code drops to 256 colours whenever TMUX is set unless told
		// the multiplexer passes truecolor through, which attach arranges.
		b.WriteString("export CLAUDE_CODE_TMUX_TRUECOLOR=1\n")
		b.WriteString(updateScript("claude", "Claude Code"))
		b.WriteString(`args+=(--settings "$D/claude-settings.json")` + "\n")
		if model != "" {
			b.WriteString(`args+=(--model ` + shellQuote(model) + `)` + "\n")
		}
		if resume {
			b.WriteString(`if ls "$HOME"/.claude/projects/*/*.jsonl >/dev/null 2>&1; then args+=(--continue); fi` + "\n")
		}
		b.WriteString(`[ -n "$p" ] && args+=("$p")` + "\n")
	case "codex":
		// Before the hook args, which check which flags this Codex has.
		b.WriteString(updateScript("codex", "Codex"))
		b.WriteString(codexHooksArgs())
		if model != "" {
			b.WriteString(`args+=(--model ` + shellQuote(model) + `)` + "\n")
		}
		if resume {
			b.WriteString(`if find "${CODEX_HOME:-$HOME/.codex}/sessions" -name '*.jsonl' 2>/dev/null | grep -q .; then args+=(resume --last); fi` + "\n")
		}
		b.WriteString(`[ -n "$p" ] && args+=("$p")` + "\n")
	default:
		// Other agents take their first instruction as typed input once
		// their UI is up.
		b.WriteString(`if [ -n "$p" ]; then (sleep 6; tmux -L ` + tmuxSocket + ` send-keys -t ` + session + ` -l "$p"; sleep 0.5; tmux -L ` + tmuxSocket + ` send-keys -t ` + session + ` Enter) & fi` + "\n")
	}
	if agentName == "shell" {
		b.WriteString(`bash -l` + "\n")
	} else {
		b.WriteString(`if [ -f /usr/local/lib/sandbox/start-agent ]; then
  /bin/bash /usr/local/lib/sandbox/start-agent "${args[@]}"
else
  ` + agentName + ` "${args[@]}"
fi
`)
	}
	// Capture the status first: "$(date)" in the same command would reset $?.
	b.WriteString(`rc=$?` + "\n")
	b.WriteString(`printf '{"ts":%s,"ev":"AgentExit","code":%s}\n' "$(date +%s)" "$rc" >> "$D/events.jsonl"` + "\n")
	return b.String()
}

// SendScript types text into the agent's prompt and submits it.
func SendScript(text string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	return fmt.Sprintf(`set -e
tmux -L %[1]s has-session -t %[2]s 2>/dev/null || { echo "agent is not running" >&2; exit 4; }
t=$(printf '%%s' '%[3]s' | base64 -d)
tmux -L %[1]s send-keys -t %[2]s -l "$t"
sleep 0.4
tmux -L %[1]s send-keys -t %[2]s Enter
`, tmuxSocket, session, enc)
}

// GitSetupScript puts a fresh clone on the branch the user asked for. A new
// branch starts from the default branch, not from whatever the host had
// checked out when the clone was made. The remote's HEAD wins over
// defaultBranch, which is only the host's guess and goes stale when its
// origin/HEAD does. It doesn't track the default branch, so a later push
// goes to a branch of its own name. An empty value checks out the default
// branch itself. A PR resets any local branch of the same name to the PR's
// head: the clone inherits the host's branches, and a stale one that has
// diverged, as after a force push, would otherwise fail the fast-forward.
// Every source also points origin/HEAD at the remote's HEAD, since the
// clone inherits the host's and agents read it to pick a PR base.
func GitSetupScript(source, value, defaultBranch string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(value))
	switch source {
	case "pr":
		return fmt.Sprintf("set -e\nv=$(printf '%%s' '%s' | base64 -d)\n%sgh pr checkout --force \"$v\" >&2\ngit branch --show-current\n", enc, syncRemoteHead)
	default:
		return fmt.Sprintf(`set -e
v=$(printf '%%s' '%s' | base64 -d)
DEF=%s
%sdefault_base() {
  [ -n "$r" ] || GIT_TERMINAL_PROMPT=0 git fetch -q origin "$DEF" 1>&2 || true
  base=""
  for b in "origin/$DEF" "$DEF"; do
    if git rev-parse -q --verify "$b^{commit}" >/dev/null 2>&1; then base=$b; break; fi
  done
  if [ -z "$base" ]; then echo "default branch $DEF not found" >&2; exit 5; fi
}
if [ -z "$v" ]; then
  default_base
  if git rev-parse -q --verify "refs/heads/$DEF" >/dev/null; then
    git checkout "$DEF" 1>&2
    git merge -q --ff-only "$base" >/dev/null 2>&1 || true
  else
    git checkout --track -b "$DEF" "$base" 1>&2
  fi
elif git rev-parse -q --verify "refs/heads/$v" >/dev/null; then
  git checkout "$v" 1>&2
else
  default_base
  git checkout --no-track -b "$v" "$base" 1>&2
fi
git branch --show-current
`, enc, shellQuote(defaultBranch), syncRemoteHead)
	}
}

// syncRemoteHead asks the remote for its HEAD, fetches that branch and points
// origin/HEAD at it, leaving the branch name in $r and $DEF. It does nothing
// when the remote can't be reached.
const syncRemoteHead = `r=$(GIT_TERMINAL_PROMPT=0 git ls-remote --symref origin HEAD 2>/dev/null | awk '$1 == "ref:" && $3 == "HEAD" { sub("^refs/heads/", "", $2); print $2 }') || true
if [ -n "$r" ]; then
  DEF=$r
  GIT_TERMINAL_PROMPT=0 git fetch -q origin "$r" 1>&2 || true
  git remote set-head origin "$r" >/dev/null 2>&1 || true
fi
`

func heredoc(b *strings.Builder, path, content string) {
	fmt.Fprintf(b, "cat > \"%s\" <<'SBXP_EOF'\n%sSBXP_EOF\n", path, content)
}

// ProbeScript gathers everything the TUI shows about a running sandbox in a
// single exec. Sections are introduced by "@@name" lines.
func ProbeScript(defaultBranch string) string {
	return fmt.Sprintf(`D="$HOME/.sbx-projects"
DEF=%s
echo "@@agent"
if command -v tmux >/dev/null 2>&1 && tmux -L %s has-session -t %s 2>/dev/null; then echo up; else echo down; fi
echo "@@clone"
if [ -d /run/sandbox/source ]; then echo yes; else echo no; fi
echo "@@events"
tail -n 200 "$D/events.jsonl" 2>/dev/null
echo "@@recap"
cat "$D/last-turn.txt" 2>/dev/null
%[4]sif git rev-parse --git-dir >/dev/null 2>&1; then
  echo "@@branch"
  git branch --show-current 2>/dev/null
  base=""
  for b in "origin/$DEF" "$DEF"; do
    if git rev-parse -q --verify "$b^{commit}" >/dev/null 2>&1; then base=$b; break; fi
  done
  if [ -n "$base" ]; then
    echo "@@base"
    echo "$base"
    echo "@@commits"
    git log --format='%%h%%x09%%ct%%x09%%s' "$base..HEAD" 2>/dev/null | head -n 50
    echo "@@diff"
    mb=$(git merge-base "$base" HEAD 2>/dev/null) && git diff --numstat "$mb" 2>/dev/null | head -n 200
  fi
fi
`, shellQuote(defaultBranch), tmuxSocket, session, codexTurnProbe)
}

// codexTurnProbe reports the last turn boundary in Codex's newest session
// log. Codex runs no Stop hook when a /review ends: the review is a
// sub-agent, and Codex skips lifecycle hooks for those. The session log
// still records the turn completing.
const codexTurnProbe = `S="${CODEX_HOME:-$HOME/.codex}/sessions"
if [ -d "$S" ] && command -v jq >/dev/null 2>&1; then
  f=$(find "$S" -name '*.jsonl' -printf '%T@ %p\n' 2>/dev/null | sort -n | tail -n 1 | cut -d' ' -f2-)
  if [ -n "$f" ]; then
    echo "@@turn"
    tail -n 500 "$f" | grep -E '"type":"(task_started|task_complete|turn_aborted)"' | tail -n 1 |
      jq -c '{ts: (.timestamp | sub("\\.[0-9]+"; "") | fromdateiso8601), ev: .payload.type}' 2>/dev/null
  fi
fi
`

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// diffMaxLines caps the patch DiffScript returns so a huge change (a
// vendored directory, a lockfile) can't stall the exec.
const diffMaxLines = 20000

// DiffScript prints the full patch behind the probe's "@@diff" numstat:
// the working tree against the merge base with the default branch. It
// prints nothing when there is no base to compare with.
func DiffScript(defaultBranch string) string {
	return fmt.Sprintf(`DEF=%s
git rev-parse --git-dir >/dev/null 2>&1 || exit 0
base=""
for b in "origin/$DEF" "$DEF"; do
  if git rev-parse -q --verify "$b^{commit}" >/dev/null 2>&1; then base=$b; break; fi
done
[ -n "$base" ] || exit 0
mb=$(git merge-base "$base" HEAD 2>/dev/null) || exit 0
git diff --no-color --no-ext-diff "$mb" 2>/dev/null | head -n %d
`, shellQuote(defaultBranch), diffMaxLines)
}
