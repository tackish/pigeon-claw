package discord

import (
	"strings"
	"testing"

	"github.com/tackish/pigeon-claw/config"
)

// Aliases hide which model actually runs — `opus` resolves to
// claude-opus-4-8, not claude-opus-5 — so the picker offers full IDs only.
func TestClaudeModelsAreFullIDsNotAliases(t *testing.T) {
	for _, list := range [][]modelChoice{claudeCLIModels, claudeAPIModels} {
		for _, m := range list {
			if !strings.HasPrefix(m.ID, "claude-") {
				t.Fatalf("%q is an alias; the picker must offer full model IDs", m.ID)
			}
			if m.Name != m.ID {
				t.Fatalf("label %q should match the ID %q so the pick is unambiguous", m.Name, m.ID)
			}
		}
	}
}

// The `[1m]` suffix is a claude CLI convention for the 1M-context variant.
// The Messages API rejects it as an unknown model, so it must never reach
// the HTTP provider's menu.
func TestAPIModelsCarryNoCLIOnlySuffix(t *testing.T) {
	for _, m := range claudeAPIModels {
		if strings.Contains(m.ID, "[") {
			t.Fatalf("%q is a CLI-only form and would 404 against the API", m.ID)
		}
	}
}

// The API requires a model on every request, so the provider must not be
// offered the "no model" option.
func TestOnlyTheCLIProviderMayBeUnset(t *testing.T) {
	if _, allowUnset := modelChoicesFor("claude-cli"); !allowUnset {
		t.Fatal("claude-cli runs without --model, so unset must be offered")
	}
	if _, allowUnset := modelChoicesFor("claude"); allowUnset {
		t.Fatal("the HTTP API rejects an empty model, so unset must not be offered")
	}
}

func TestModelChoicesOnlyForClaudeProviders(t *testing.T) {
	if got, _ := modelChoicesFor("claude-cli"); len(got) == 0 {
		t.Fatal("claude-cli should have a curated list")
	}
	if got, _ := modelChoicesFor("ollama"); got != nil {
		t.Fatalf("providers without a curated list must fall back to !model, got %v", got)
	}
}

func TestDisplayModelNamesTheDefault(t *testing.T) {
	if got := displayModel(""); got != modelDefaultLabel {
		t.Fatalf("unpinned model should read as %q, got %q", modelDefaultLabel, got)
	}
	if got := displayModel("claude-opus-5"); got != "claude-opus-5" {
		t.Fatalf("a pinned model shows as itself, got %q", got)
	}
}

// The default has to be a model the picker also offers, or the menu will
// show a state the user cannot pick again after changing it.
func TestDefaultModelIsOfferedByThePicker(t *testing.T) {
	for _, m := range claudeCLIModels {
		if m.ID == config.DefaultClaudeCLIModel {
			return
		}
	}
	t.Fatalf("default model %q is not in the picker list", config.DefaultClaudeCLIModel)
}
