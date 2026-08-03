package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/tackish/pigeon-claw/router"
)

// maxSummaryTools caps how many distinct tools the footer names, so a long
// agentic run doesn't bury the summary under a tool list.
const maxSummaryTools = 6

// requestSummary renders the closing line of a reply: how long the request
// took, what it cost, and which tools ran. A reply that ends on the model's
// last sentence reads as if it were cut off — this says the work is done and
// what it consisted of.
func requestSummary(result *router.HandleResult, elapsed time.Duration, delivered bool) string {
	// Claiming success under an empty reply is worse than saying nothing:
	// the user reads "완료" and goes looking for an answer that was never
	// sent. Providers can return success with no content.
	head := "✅ 완료"
	if !delivered {
		head = "⚠️ 완료 — 응답 내용이 비어 있습니다"
	}
	parts := []string{head, elapsed.Truncate(time.Second).String()}

	// Which model answered. Nothing is pinned by default, so this is the
	// only place the actual model shows up.
	switch {
	case result.Provider != "" && result.Model != "":
		parts = append(parts, result.Provider+" · "+result.Model)
	case result.Provider != "":
		parts = append(parts, result.Provider)
	case result.Model != "":
		parts = append(parts, result.Model)
	}
	if result.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("%s tokens", formatCount(result.TotalTokens)))
	}

	summary := "-# " + strings.Join(parts, " | ")
	if tools := formatTools(result.ToolsRun, result.ToolsUsed); tools != "" {
		summary += "\n-# " + tools
	}
	return summary
}

// formatTools renders the tool breakdown, e.g. "🔧 Bash ×5 · Read ×3 (+2 more)".
// Falls back to a bare count for providers that report no breakdown.
func formatTools(tools []router.ToolUse, total int) string {
	if len(tools) == 0 {
		if total > 0 {
			return fmt.Sprintf("🔧 tool %d회", total)
		}
		return ""
	}

	shown := tools
	hidden := 0
	if len(shown) > maxSummaryTools {
		hidden = len(shown) - maxSummaryTools
		shown = shown[:maxSummaryTools]
	}

	names := make([]string, 0, len(shown))
	for _, t := range shown {
		if t.Count > 1 {
			names = append(names, fmt.Sprintf("%s ×%d", t.Name, t.Count))
			continue
		}
		names = append(names, t.Name)
	}

	out := "🔧 " + strings.Join(names, " · ")
	if hidden > 0 {
		out += fmt.Sprintf(" (+%d more)", hidden)
	}
	return out
}

// formatCount abbreviates large token counts: 1234 → "1.2k".
func formatCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}
