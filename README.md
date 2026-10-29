# sbx-projects

Manage your coding agents in sandboxes through the `sbx` CLI from one terminal. Group work by project, see which agents need your attention, review changes, and jump into a conversation without losing track of your other tasks.

![sbx-projects demo showing projects, sandbox status, and the selected agent’s activity](docs/images/sbx-projects-demo.png)

*The built-in demo: projects on the left, sandboxes in the middle, and activity for the selected sandbox on the right.*

## Get started

You’ll need **Go 1.26 or newer** to install or build. For real projects, install and configure the `sbx` CLI and make sure it is on your `PATH`.

Install directly with Go:

```sh
go install github.com/cmrigney/sbx-projects/cmd/sbx-projects@latest
```

Add Go’s binary directory to your `PATH` (`$(go env GOPATH)/bin` by default, or your custom `GOBIN`), then try `sbx-projects --demo`.

Or, from this repository, build and try the demo:

```sh
go build -o sbx-projects ./cmd/sbx-projects
./sbx-projects --demo
```

The demo uses built-in data and an in-memory database—no `sbx` setup is needed. Press `?` to explore the shortcuts and `q` to quit.

To use your own projects, put the binary on your `PATH`, then launch it inside a git repository:

```sh
cd /path/to/your/repo
sbx-projects
```

On the first launch, that repository is added automatically. Press `A` (Shift+A) to add more projects.

## Your first sandbox

1. Select a project and press `n` to create a sandbox.
2. Choose your agent and a branch or pull request. Use `Tab` to move between fields and the arrow keys to choose options. Expand the model, kits, and MCP summary with `Space` if you want to customize them.
3. Press `Enter` to create the sandbox and start the agent.
4. Select the sandbox and press `Enter` to attach. To return to the dashboard, press **Ctrl+B, release, then d**. The agent keeps running.
5. Back in the dashboard, press `t` to read the latest reply, or `i` to send another instruction.

A **project** is a git repository. The per-feature layout lets you run separate tasks in private clones and branches; press `m` to switch to one sandbox for the repository. Sandboxes run detached and keep running after you quit the dashboard, until you stop them.

## Shortcuts worth learning

Keys are case-sensitive. Sandbox actions apply to the selected sandbox; move out of the Projects pane with `Tab` before attaching.

### Work with an agent

| Key | Action |
|---|---|
| `n` | Create a sandbox |
| `Enter` | Attach to the agent, starting it if needed |
| **Ctrl+B, then d** | Detach from the agent and return to the dashboard |
| `i` | Send the agent a message without attaching |
| `t` | Read the agent’s last reply in full |
| `2`, then `d` | Open Changes, then view the full diff |
| `p` | Ask the agent to open a **draft** pull request |
| `o` | Open the sandbox’s private clone in your IDE over SSH |
| `c` | Fetch the private clone’s branch into your local checkout and check it out |
| `$` | Open a shell in the sandbox |
| `P` / `U` | Publish / unpublish a port (`sbx ports`) |
| `s` / `r` / `x` | Stop / resume / remove the sandbox |

An orange **●** and bold sandbox title mean there’s an unread reply. Read it with `t` or attach to clear the marker. In reply and diff views, use `j`/`k` to scroll, `Space`/`PgDown` to page down, and `Esc` to close. In a diff, `[`/`]` move between files.

### Navigate and manage projects

| Key | Action |
|---|---|
| `Tab` / `Shift+Tab` | Next / previous pane |
| `↑` / `↓` or `k` / `j` | Move through a list or scroll details |
| `←` / `→` or `h` / `l` | Switch detail tabs or panes |
| `[` / `]` | Previous / next project |
| `1` / `2` / `3` | Activity / Changes / Setup |
| `A` | Add a project |
| `D` | Forget the current project |
| `m` | Switch between per-feature and per-repository layouts |
| `K` | Set default kits for new sandboxes |
| `e` | Open the project in your local editor |
| `L` | Show the debug log |
| `?` | Show the keyboard reference |
| `q` | Quit the dashboard |

## Customize your workflow

**Default kits.** Press `K` to set the kits every new sandbox in the current project starts with. Each reference (a directory, ZIP, git URL or OCI image) must be a mixin, and is passed to `sbx run` as its own `--kit`. The New sandbox form starts with the project's kits filled in, so you can add or drop kits for one sandbox. Changing the defaults doesn't touch existing sandboxes. The Setup tab shows the kits each sandbox was created with.

**Ports.** Press `P` to forward a port on your machine to the sandbox with `sbx ports --publish`. Type it as `[[HOST_IP:]HOST_PORT:]SANDBOX_PORT[/PROTOCOL]`; a lone number such as `3000` uses the same port on both sides. Press `U` and give a host port to stop forwarding it. The Setup tab lists the published ports. To reach a port from your phone or another device, publish it as `0.0.0.0:3000:3000` and have the server in the sandbox listen on `0.0.0.0`.

**MCP servers.** The New sandbox form lists the MCP servers registered on your host (`sbx mcp ls --json`). Move along the row with ←→ and press space to pick a server; picked servers are passed to `sbx run` as `--static-mcp`. sbx fixes that set when the sandbox is created, so it can't be changed later from here. Servers that can't connect yet show why (for example "needs auth"), and the form names the command that fixes the one under the cursor. Register servers with `sbx mcp add` on your host. The form starts with the servers you last picked in the project, and the Setup tab shows the ones each sandbox was created with.

**Agent model.** When the agent is Claude Code or Codex, the New sandbox form has a Model row. Claude offers Default, Opus, Sonnet and Haiku; Codex offers Default and OpenAI's recommended Codex models. The agent starts with `--model` set to that choice, and keeps it whenever it starts or resumes; Default passes no flag. The form remembers the last model picked for each agent in the project.

**Folded rows.** The New sandbox form folds the Model, Kits and MCP servers rows into one line that sums up what the sandbox will get (for example "Opus model · 2 kits · notion"). Tab to it and press space, or click it, to show the rows.

**Open in editor.** `e` opens the project in your IDE. It uses `$SBX_PROJECTS_EDITOR` if it's set (for example `export SBX_PROJECTS_EDITOR="code -n"`). Otherwise it uses the first IDE launcher it finds on `PATH` (`cursor`, `code`, `zed`, `windsurf`, `goland`, `idea`, `subl`). On macOS it also checks for those apps in `/Applications` and opens them with `open -a`. If no IDE turns up, it uses `$VISUAL` or `$EDITOR` in the terminal, and then the system file opener.

### Command-line options and data

| Option | Purpose |
|---|---|
| `--demo` | Explore built-in demo data without `sbx` |
| `--db PATH` | Use a different projects database |
| `--sbx PATH` | Use a specific `sbx` binary |
| `--log PATH` | Write the debug log to `PATH`, or pass `off` to turn it off (also `$SBX_PROJECTS_LOG`) |
| `--log-level LEVEL` | Log `debug`, `info` (the default), `warn` or `error` and above |

Projects and sandbox metadata are stored in SQLite at `~/.local/share/sbx-projects/projects.db`, or `$XDG_DATA_HOME/sbx-projects/projects.db` when set. Demo mode does not use this database.

**Debug log.** Each run writes a new log to the `sbx-projects` folder in your temp directory, and logs older than a week are deleted. The path is printed when the dashboard starts, again if it exits with an error, and at the bottom of the `?` help. Press `L` to read the latest 500 entries without leaving the dashboard: it follows new entries as they arrive, puts each entry's details on their own lines, and `e` switches to only warnings and errors. The log records every error and status message, every `sbx` command that changes a sandbox, and the full error output of any `sbx` command that fails. With `--log-level debug` it also records the listing and status checks that run every few seconds. Scripts sent to sandboxes aren't logged, since they can contain your prompts.

## Requirements and limits

- tmux is installed with `apt-get` the first time an agent starts in a sandbox. That needs the Ubuntu archive to be reachable under your network policy.
- Sandboxes whose workspace is inside a project are adopted automatically. If an adopted sandbox's agent was started with plain `sbx run`, the TUI doesn't see it. Attaching starts a second agent in tmux.
- Changes for a stopped sandbox show what was last seen while it was running. Probing a stopped sandbox would start it.
- Open PR asks the agent to push and run `gh pr create`. This needs a GitHub token stored with `sbx secret set github`.

## How it works

Everything goes through the `sbx` CLI.

| Action | What runs |
|---|---|
| Create | `sbx run -d --name N [--clone] [--kit REF]... [--static-mcp NAME,...] AGENT PATH`, then `git checkout -b BRANCH` (or `gh pr checkout N`) in the clone, then the agent starts |
| Start agent | tmux on a private socket (`tmux -L sbxp`) inside the sandbox runs sbx's own `start-agent` launcher, so the agent keeps working with nothing attached |
| Attach (`enter`) | `sbx exec -it N tmux -u -L sbxp attach -t agent` (adding `-T RGB` when your terminal sets `COLORTERM=truecolor`). Press `ctrl-b d` to come back |
| Message (`i`), Open PR (`p`) | Your text is typed into the agent's session with `tmux send-keys` |
| Check out locally (`c`) | On a private clone, `git fetch sandbox-N` then `git checkout -B BRANCH sandbox-N/BRANCH` in the project. sandboxd configures that remote automatically while the sandbox is running. |
| Open in IDE (`o`) | On a private clone, `cursor --remote ssh-remote+N.sbx WORKSPACE` (or `code`, or whatever `$SBXP_IDE` names), the same as Remote-SSH: Connect to Host with sbx's `N.sbx` host |
| Status, activity, changes | One `sbx exec` every few seconds per *running* sandbox reads the event log, `git log` and `git diff --numstat` against the project's default branch |
| Stop / resume / remove | `sbx stop`, `sbx run -d --name N` then the agent with `--continue`, `sbx rm -f` (after `sbx stop` if it is running) |

Sandboxes are created detached, so they keep running until you stop them.

<details>
<summary>How agent status and hooks work</summary>

**Claude Code and Codex status** comes from hooks. Each hook appends a line to `~/.sbx-projects/events.jsonl` inside the sandbox. sbx rewrites both agents' own config files, so neither is touched:

- **Claude Code** is launched with `--settings ~/.sbx-projects/claude-settings.json`, which adds hooks for `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUseFailure`, `Notification`, `Stop`, `StopFailure` and `SessionEnd`.
- **Codex** gets its hooks as a `-c hooks={…}` override plus `--dangerously-bypass-hook-trust`, which hooks from overrides need in order to run. Its events are `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, `Stop`, `Interrupt` and `SessionEnd`. The hook reads the files out of `apply_patch` patches, and looks up a failed shell command's exit code in Codex's session log, because Codex's `PostToolUse` carries only the output. The workspace is also pre-trusted with `-c projects={…}`, as sbx does for Claude, so Codex doesn't stop at its trust prompt. Codex versions without `--dangerously-bypass-hook-trust` start without hooks.

Other agents only report whether their session is running.

</details>
