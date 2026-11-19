package core

import (
	"strings"

	"github.com/cmrigney/sbx-projects/internal/store"
)

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

// CodexModels are the Codex model families recommended by OpenAI. Default
// leaves selection to Codex, so its default can continue to evolve.
var CodexModels = []Model{
	{"", "Default"}, {"astra", "Astra"}, {"sol", "Sol"}, {"terra", "Terra"}, {"luna", "Luna"},
}

// codexLatest maps each Codex family to its newest model. Codex has no
// family aliases like Claude Code's, so a pick stores the family and is
// resolved here when the agent starts; bump these when OpenAI releases.
var codexLatest = map[string]string{
	"astra": "gpt-6.2-astra",
	"sol":   "gpt-6.2-sol",
	"terra": "gpt-6.1-terra",
	"luna":  "gpt-6-luna",
}

// codexFamily is the family of a Codex pick, accepting both a family and
// a versioned ID such as gpt-6.1-sol remembered before families, or ""
// when the pick isn't in a known family.
func codexFamily(id string) string {
	if _, ok := codexLatest[id]; ok {
		return id
	}
	if i := strings.LastIndex(id, "-"); i >= 0 && strings.HasPrefix(id, "gpt-") {
		if _, ok := codexLatest[id[i+1:]]; ok {
			return id[i+1:]
		}
	}
	return ""
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
	id = PickedModel(agentName, id)
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
		if f := codexFamily(rec.CodexModel); f != "" {
			return codexLatest[f]
		}
		return rec.CodexModel
	}
	return ""
}

// PickedModel is a remembered pick as one of the agent's choices, so a
// versioned Codex ID from before families selects its family.
func PickedModel(agentName, id string) string {
	if agentName == "codex" {
		if f := codexFamily(id); f != "" {
			return f
		}
	}
	return id
}
