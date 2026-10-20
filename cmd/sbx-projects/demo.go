package main

import (
	"fmt"
	"time"

	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

const demoStartPath = "/Users/you/Development/skill-share"

func seedDemo(st *store.Store) *sbx.Mock {
	home := "/Users/you/Development"
	add := func(name, path, remote string, layout store.Layout) store.Project {
		p, _ := st.AddProject(store.Project{Name: name, Path: path, Remote: remote, Layout: layout})
		return p
	}
	skills := add("skill-share", home+"/skill-share", "github.com/cmrigney/skill-share", store.LayoutFeature)
	extension := add("wasm-docker-extension", home+"/wasm-docker-extension", "github.com/cmrigney/wasm-docker-extension", store.LayoutRepo)
	sftp := add("node-sftp-s3", "/Users/you/code/node-sftp-s3", "github.com/cmrigney/node-sftp-s3", store.LayoutFeature)

	now := time.Now().Unix()
	at := func(minAgo int) int64 { return now - int64(minAgo*60) }
	ev := func(minAgo int, kv ...any) map[string]any {
		e := map[string]any{"ts": at(minAgo)}
		for i := 0; i+1 < len(kv); i += 2 {
			e[kv[i].(string)] = kv[i+1]
		}
		return e
	}
	commit := func(minAgo int, hash, msg string) string { return fmt.Sprintf("%s\t%d\t%s", hash, at(minAgo), msg) }

	type rec struct {
		project store.Project
		title   string
		seed    sbx.MockSeed
	}
	recs := []rec{
		{skills, "Validate skill metadata before publishing", sbx.MockSeed{
			Name: "claude-skill-validation", Agent: "claude", Workspace: skills.Path, Branch: "feat/skill-validation",
			Running: true, AgentUp: true, Clone: true, CPUs: 4, MemoryMiB: 8192, Age: 2 * time.Minute,
			Events: []map[string]any{
				ev(62, "ev", "SessionStart", "reason", "startup"),
				ev(62, "ev", "UserPromptSubmit", "prompt", "Validate skill metadata before publishing and keep go test ./... green"),
				ev(50, "ev", "PreToolUse", "tool", "Read", "file", skills.Path+"/internal/skills/validate.go"),
				ev(49, "ev", "PreToolUse", "tool", "Read", "file", skills.Path+"/internal/skills/metadata.go"),
				ev(48, "ev", "PreToolUse", "tool", "Read", "file", skills.Path+"/cmd/push.go"),
				ev(34, "ev", "PreToolUse", "tool", "Bash", "cmd", `git commit -m "feat(skills): validate required metadata"`),
				ev(23, "ev", "PreToolUse", "tool", "Bash", "cmd", "go test ./...", "desc", "Run the unit tests"),
				ev(6, "ev", "PreToolUse", "tool", "Bash", "cmd", `git commit -m "fix(skills): report invalid metadata fields"`),
				ev(2, "ev", "Notification", "ntype", "elicitation_dialog", "msg", "Should missing descriptions block publishing, or produce a warning?"),
			},
			Commits: []string{commit(34, "39b0eb5", "feat(skills): validate required metadata"), commit(6, "33f714d", "fix(skills): report invalid metadata fields")},
			Files:   []string{"341\t122\tinternal/skills/metadata.go", "188\t290\tinternal/skills/validate.go", "214\t0\tinternal/skills/validate_test.go", "69\t192\tcmd/push.go"},
		}},
		{skills, "Retry interrupted skill downloads", sbx.MockSeed{
			Name: "claude-download-retry", Agent: "claude", Workspace: skills.Path, Branch: "feat/download-retry",
			Running: true, AgentUp: true, Clone: true, CPUs: 2, MemoryMiB: 4096,
			Events: []map[string]any{
				ev(30, "ev", "SessionStart", "reason", "startup"),
				ev(30, "ev", "UserPromptSubmit", "prompt", "Retry interrupted skill downloads"),
				ev(24, "ev", "PreToolUse", "tool", "Grep", "pattern", "Retry"),
				ev(9, "ev", "PreToolUse", "tool", "Bash", "cmd", `git commit -m "feat(pull): retry interrupted downloads"`),
				ev(0, "ev", "PreToolUse", "tool", "Edit", "file", skills.Path+"/internal/registry/pull_test.go"),
			},
			Commits: []string{commit(9, "a41c2e0", "feat(pull): retry interrupted downloads")},
			Files:   []string{"92\t18\tinternal/registry/pull.go", "54\t4\tinternal/registry/pull_test.go"},
			Script: []map[string]any{
				{"ev": "PreToolUse", "tool": "Bash", "cmd": "go test ./internal/registry/...", "desc": "Run the registry tests"},
				{"ev": "PreToolUse", "tool": "Edit", "file": skills.Path + "/internal/registry/pull.go"},
				{"ev": "PreToolUse", "tool": "Bash", "cmd": "go test ./internal/registry/...", "desc": "Run the registry tests again"},
				{"ev": "Stop", "msg": "Interrupted downloads now retry from the last completed layer. Tests pass.\n\n## What changed\n- `pull.go` retries downloads from the last completed layer.\n- `pull_test.go` covers transient failures and exhausted retries.\n\n| Case | Before | After |\n|---|---|---|\n| Transient failure | Aborted the pull | Retries the download |\n| Retry limit reached | No retry context | Reports the last error |\n\nOne question: should the retry limit be configurable? I used three attempts."},
			},
		}},
		{skills, "Explain skill publishing in the CLI help", sbx.MockSeed{
			Name: "gemini-docs-publish", Agent: "gemini", Workspace: skills.Path, Branch: "docs/publish-help",
			Running: true, AgentUp: true, Clone: true, CPUs: 2, MemoryMiB: 4096, Age: 40 * time.Minute,
		}},
		{skills, "Review: pull errors include the skill reference", sbx.MockSeed{
			Name: "claude-pr-4127", Agent: "claude", Workspace: skills.Path, Branch: "fix/pull-error-reference",
			Running: true, AgentUp: true, Clone: true, CPUs: 2, MemoryMiB: 4096, Age: 6 * time.Minute,
			Events: []map[string]any{
				ev(20, "ev", "SessionStart", "reason", "startup"),
				ev(20, "ev", "UserPromptSubmit", "prompt", "Review the pull request checked out on this branch."),
				ev(14, "ev", "PreToolUse", "tool", "Bash", "cmd", "go test ./cmd/...", "desc", "Run the command tests"),
				ev(6, "ev", "Stop", "msg", "Drafted 3 review comments. The error drops the skill reference when the registry request times out."),
			},
		}},
		{skills, "Resolve skill paths on Windows", sbx.MockSeed{
			Name: "claude-windows-paths", Agent: "claude", Workspace: skills.Path, Branch: "fix/windows-paths",
			Running: true, AgentUp: false, Clone: true, CPUs: 2, MemoryMiB: 4096, Age: 3 * time.Hour,
			Events: []map[string]any{
				ev(200, "ev", "PreToolUse", "tool", "Bash", "cmd", `git commit -m "fix(paths): normalize Windows separators"`),
				ev(185, "ev", "PreToolUse", "tool", "Bash", "cmd", "go test ./...", "desc", "Run the unit tests"),
				ev(184, "ev", "PostToolUseFailure", "tool", "Bash", "msg", "2 tests failed in paths_windows_test.go"),
				ev(180, "ev", "AgentExit", "code", 1),
			},
			Commits: []string{commit(200, "7c1d9a2", "fix(paths): normalize Windows separators")},
			Files:   []string{"57\t9\tinternal/skills/paths.go"},
		}},
		{skills, "Add --dry-run to skill publishing", sbx.MockSeed{
			Name: "claude-publish-dry-run", Agent: "claude", Workspace: skills.Path, Branch: "feat/publish-dry-run",
			Clone: true, Age: 50 * time.Hour,
			Events: []map[string]any{
				ev(3000, "ev", "PreToolUse", "tool", "Bash", "cmd", "gh pr create --draft --fill"),
				ev(2999, "ev", "Stop", "msg", "Opened #4098"),
			},
			Commits: []string{commit(3010, "5d0e1aa", "feat(push): add --dry-run")},
			Files:   []string{"120\t8\tcmd/push.go"},
		}},
		{extension, "Working sandbox for wasm-docker-extension", sbx.MockSeed{
			Name: "claude-wasm-extension", Agent: "claude", Workspace: extension.Path, Branch: "main",
			Running: true, AgentUp: true, CPUs: 6, MemoryMiB: 12288, Age: 12 * time.Minute,
			Ports: []sbx.Port{{HostPort: 5173, SandboxPort: 5173, Protocol: "tcp"}},
			Events: []map[string]any{
				ev(240, "ev", "SessionStart", "reason", "resume"),
				ev(16, "ev", "PreToolUse", "tool", "Edit", "file", extension.Path+"/ui/src/App.tsx"),
				ev(14, "ev", "PreToolUse", "tool", "Bash", "cmd", "npm --prefix ui run build", "desc", "Build the extension UI"),
				ev(12, "ev", "Stop", "msg", "The UI now shows a loading indicator while the Wasm backend responds."),
				ev(11, "ev", "Notification", "ntype", "idle_prompt", "msg", "Claude is waiting for your input"),
			},
			Files: []string{"24\t3\tui/src/App.tsx"},
		}},
		{sftp, "Document S3 server configuration", sbx.MockSeed{
			Name: "claude-node-sftp-s3", Agent: "claude", Workspace: sftp.Path, Branch: "docs/server-configuration",
			Clone: true, Age: 5 * 24 * time.Hour,
			Ports:   []sbx.Port{{HostPort: 2222, SandboxPort: 2222, Protocol: "tcp"}},
			Commits: []string{commit(7300, "e19ab03", "docs: explain S3 server configuration")},
			Files:   []string{"188\t0\tREADME.md", "113\t45\t.env.sample"},
		}},
	}

	var seeds []sbx.MockSeed
	for _, r := range recs {
		seeds = append(seeds, r.seed)
		_ = st.UpsertSandbox(store.Sandbox{
			Name: r.seed.Name, ProjectID: r.project.ID, Title: r.title, Branch: r.seed.Branch, Agent: r.seed.Agent,
			Clone: r.seed.Clone, Source: "branch", CreatedAt: time.Now().Add(-r.seed.Age - time.Hour),
		})
	}
	m := sbx.NewMock(seeds)
	// Stopped sandboxes are never probed, so give them the activity they
	// had when they last ran.
	for _, r := range recs {
		if !r.seed.Running {
			_ = st.SaveProbe(r.seed.Name, sbx.MockProbe(r.seed), time.Now().Add(-r.seed.Age))
		}
	}
	return m
}
