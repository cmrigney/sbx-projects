package core

import "github.com/cmrigney/sbx-projects/internal/store"

// Model is one choice for an agent's --model. An empty ID leaves the
// choice to the agent.
type Model struct {
	ID, Label string
}

// ClaudeModels are Claude Code's model aliases, which track the latest
// release of each family.
var ClaudeModels = []Model{
	{"", "Default"}, {"opus", "Opus"}, {"sonnet", "Sonnet"}, {"haiku", "Haiku"},
}

// CodexModels are the models recommended for Codex by OpenAI. Default
// leaves selection to Codex, so its default can continue to evolve.
var CodexModels = []Model{
	{"", "Default"}, {"gpt-6-astra", "GPT-6 Astra"}, {"gpt-6.1-sol", "GPT-6.1 Sol"}, {"gpt-6-luna", "GPT-6 Luna"},
}

// ModelsFor lists an agent's models, or nil when it has no choice.
func ModelsFor(agentName string) []Model {
	switch agentName {
	case "claude":
		return ClaudeModels
	case "codex":
		return CodexModels
	}
	return nil
}

// ModelLabel is the model's name in the UI, or its ID when it isn't one
// of the agent's choices.
func ModelLabel(agentName, id string) string {
	for _, c := range ModelsFor(agentName) {
		if c.ID == id {
			return c.Label
		}
	}
	return id
}

// SandboxModel is the --model a sandbox's agent starts with.
func SandboxModel(rec *store.Sandbox) string {
	switch rec.Agent {
	case "claude":
		return rec.ClaudeModel
	case "codex":
		return rec.CodexModel
	}
	return ""
}
