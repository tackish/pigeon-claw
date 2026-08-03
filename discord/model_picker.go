package discord

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/tackish/pigeon-claw/provider"
)

// modelSelectPrefix marks a select-menu interaction as a model change.
// The provider name follows it: "model:claude-cli".
const modelSelectPrefix = "model:"

// modelDefaultValue clears the model override. Discord rejects an empty
// option value, so the menu carries this sentinel instead.
const modelDefaultValue = "__default__"

// modelDefaultLabel names an unpinned model wherever one is displayed.
const modelDefaultLabel = "CLI 기본값"

// displayModel renders a provider's configured model for the UI. Empty
// means nothing is pinned and the provider picks for itself.
func displayModel(model string) string {
	if model == "" {
		return modelDefaultLabel
	}
	return model
}

// modelChoice is one entry in a provider's picker.
type modelChoice struct {
	ID   string // value passed to SetModel — a CLI alias or a full model ID
	Name string // label shown in the menu
	Desc string // one-line hint under the label
}

// claudeCLIModels are offered for the claude-cli provider.
//
// Full IDs only — no aliases. An alias hides which model actually runs and
// does not track what you would expect: `opus` resolves to claude-opus-4-8,
// not claude-opus-5. The `[1m]` suffix selects the 1M-context variant; every
// entry below was verified against claude CLI 2.1.220.
var claudeCLIModels = []modelChoice{
	{ID: "claude-fable-5", Name: "claude-fable-5", Desc: "가장 강력 — 긴 작업, 1M 컨텍스트 기본"},
	{ID: "claude-opus-5[1m]", Name: "claude-opus-5[1m]", Desc: "복잡한 코딩·에이전트 작업, 1M 컨텍스트"},
	{ID: "claude-opus-5", Name: "claude-opus-5", Desc: "복잡한 코딩·에이전트 작업"},
	{ID: "claude-opus-4-8[1m]", Name: "claude-opus-4-8[1m]", Desc: "이전 세대 Opus, 1M 컨텍스트"},
	{ID: "claude-opus-4-8", Name: "claude-opus-4-8", Desc: "이전 세대 Opus"},
	{ID: "claude-sonnet-5[1m]", Name: "claude-sonnet-5[1m]", Desc: "빠르고 저렴, 1M 컨텍스트"},
	{ID: "claude-sonnet-5", Name: "claude-sonnet-5", Desc: "빠르고 저렴 — 일상 작업"},
	{ID: "claude-haiku-4-5", Name: "claude-haiku-4-5", Desc: "가장 빠르고 저렴"},
}

// claudeAPIModels are offered for the `claude` provider, which posts to the
// Messages API rather than running the CLI.
//
// Deliberately not the CLI list: the `[1m]` suffix is a CLI convention for
// selecting the 1M-context variant, and the API rejects it as an unknown
// model. The API also has no notion of "no model" — the field is required —
// so this provider gets no unset option either (see picker below).
var claudeAPIModels = []modelChoice{
	{ID: "claude-fable-5", Name: "claude-fable-5", Desc: "가장 강력 — 긴 작업"},
	{ID: "claude-opus-5", Name: "claude-opus-5", Desc: "복잡한 코딩·에이전트 작업"},
	{ID: "claude-opus-4-8", Name: "claude-opus-4-8", Desc: "이전 세대 Opus"},
	{ID: "claude-sonnet-5", Name: "claude-sonnet-5", Desc: "빠르고 저렴 — 일상 작업"},
	{ID: "claude-haiku-4-5", Name: "claude-haiku-4-5", Desc: "가장 빠르고 저렴"},
}

// modelChoicesFor returns the picker entries for a provider and whether that
// provider accepts having no model set. Returns nil for providers with no
// curated list — their model is set with `!model <provider> <model>`.
func modelChoicesFor(providerName string) (choices []modelChoice, allowUnset bool) {
	switch providerName {
	case "claude-cli":
		// Unset is meaningful here: it passes no --model and lets the CLI
		// run whatever it is configured for.
		return claudeCLIModels, true
	case "claude":
		return claudeAPIModels, false
	}
	return nil, false
}

// sendModelPicker posts the current model of every provider plus a select
// menu per provider that has a curated list.
func (h *Handler) sendModelPicker(s *discordgo.Session, channelID string) {
	var sb strings.Builder
	sb.WriteString("**Models**\n")

	var rows []discordgo.MessageComponent
	for _, p := range h.router.GetProviders() {
		current := p.Model()
		sb.WriteString(fmt.Sprintf("- %s: `%s`\n", p.Name(), displayModel(current)))

		choices, allowUnset := modelChoicesFor(p.Name())
		if len(choices) == 0 || len(rows) >= 5 {
			// Discord allows at most 5 action rows per message.
			continue
		}

		options := make([]discordgo.SelectMenuOption, 0, len(choices)+2)
		// Offered only where an unset model means something. On the HTTP
		// API the model field is required, so picking "unset" there would
		// break every later request.
		if allowUnset {
			options = append(options, discordgo.SelectMenuOption{
				Label:       modelDefaultLabel,
				Value:       modelDefaultValue,
				Description: "모델을 지정하지 않고 CLI 설정을 따름",
				Default:     current == "",
			})
		}
		known := false
		for _, c := range choices {
			if c.ID == current {
				known = true
			}
			options = append(options, discordgo.SelectMenuOption{
				Label:       c.Name,
				Value:       c.ID,
				Description: c.Desc,
				Default:     c.ID == current,
			})
		}
		// A model set from config or `!model` may not be in the list —
		// keep it visible so the menu shows what is actually running.
		if !known && current != "" {
			options = append(options, discordgo.SelectMenuOption{
				Label:       current,
				Value:       current,
				Description: "현재 설정된 모델",
				Default:     true,
			})
		}

		rows = append(rows, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    modelSelectPrefix + p.Name(),
					Placeholder: fmt.Sprintf("%s 모델 선택", p.Name()),
					Options:     options,
				},
			},
		})
	}

	if len(rows) == 0 {
		sb.WriteString("\n" + h.msgs.ModelUsage)
	}

	if _, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:    sb.String(),
		Components: rows,
	}); err != nil {
		slog.Warn("failed to send model picker", "error", err)
		s.ChannelMessageSend(channelID, sb.String())
	}
}

// handleModelSelect applies a pick from the model select menu. Returns false
// when the interaction is not one of ours.
func (h *Handler) handleModelSelect(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	data := i.MessageComponentData()
	if !strings.HasPrefix(data.CustomID, modelSelectPrefix) {
		return false
	}
	providerName := strings.TrimPrefix(data.CustomID, modelSelectPrefix)
	if len(data.Values) == 0 {
		// Every path out of here must answer the interaction, or Discord
		// shows the click as failed.
		h.respondToComponent(s, i, "선택된 모델이 없습니다.")
		return true
	}
	model := data.Values[0]
	if model == modelDefaultValue {
		model = "" // clear the override; the provider picks for itself
	}

	var target provider.Provider
	for _, p := range h.router.GetProviders() {
		if p.Name() == providerName {
			target = p
			break
		}
	}
	if target == nil {
		h.respondToComponent(s, i, fmt.Sprintf(h.msgs.ProviderNotFound, providerName))
		return true
	}

	target.SetModel(model)
	slog.Info("model changed via picker", "provider", providerName, "model", model)
	h.respondToComponent(s, i, fmt.Sprintf(h.msgs.ModelChanged, providerName, displayModel(model)))
	return true
}

// respondToComponent acknowledges a component interaction with a visible
// message, so the picker click leaves a record of what changed.
func (h *Handler) respondToComponent(s *discordgo.Session, i *discordgo.InteractionCreate, text string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: "-# " + text},
	})
	if err != nil {
		slog.Warn("model picker response failed", "error", err)
	}
}
