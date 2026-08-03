package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/tackish/pigeon-claw/router"
)

func TestRequestSummaryIncludesToolBreakdown(t *testing.T) {
	got := requestSummary(&router.HandleResult{
		Provider:    "claude-cli",
		TotalTokens: 12345,
		ToolsUsed:   9,
		ToolsRun: []router.ToolUse{
			{Name: "Bash", Count: 5},
			{Name: "Read", Count: 3},
			{Name: "Edit", Count: 1},
		},
	}, 125*time.Second, true)

	for _, want := range []string{"완료", "2m5s", "claude-cli", "12.3k tokens", "Bash ×5", "Read ×3", "Edit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "Edit ×1") {
		t.Fatalf("a single call should not show a count: %q", got)
	}
}

// A response that produced no tokens and ran no tools still gets a closing
// line — that is the whole point of the summary.
func TestRequestSummaryAlwaysReports(t *testing.T) {
	got := requestSummary(&router.HandleResult{Provider: "claude-cli"}, 3*time.Second, true)
	if !strings.Contains(got, "완료") || !strings.Contains(got, "3s") {
		t.Fatalf("summary should report completion and elapsed time: %q", got)
	}
	if strings.Contains(got, "🔧") {
		t.Fatalf("no tools ran, so no tool line: %q", got)
	}
}

func TestFormatToolsCapsTheList(t *testing.T) {
	tools := make([]router.ToolUse, 0, maxSummaryTools+3)
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		tools = append(tools, router.ToolUse{Name: n, Count: 1})
	}

	got := formatTools(tools, 9)
	if !strings.Contains(got, "(+3 more)") {
		t.Fatalf("expected the overflow count, got %q", got)
	}
	if strings.Contains(got, "g") {
		t.Fatalf("tools past the cap should be hidden: %q", got)
	}
}

// Providers whose tools run through the executor report only a total.
func TestFormatToolsFallsBackToCount(t *testing.T) {
	if got := formatTools(nil, 4); !strings.Contains(got, "4") {
		t.Fatalf("expected a bare count, got %q", got)
	}
	if got := formatTools(nil, 0); got != "" {
		t.Fatalf("no tools means no tool line, got %q", got)
	}
}

func TestFormatCount(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{999, "999"},
		{1000, "1.0k"},
		{12345, "12.3k"},
	} {
		if got := formatCount(tc.in); got != tc.want {
			t.Fatalf("formatCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A provider can return success with nothing in it. Reporting that as a
// plain "완료" sends the user hunting for an answer that was never posted.
func TestRequestSummaryFlagsAnEmptyReply(t *testing.T) {
	got := requestSummary(&router.HandleResult{Provider: "claude"}, time.Second, false)
	if strings.Contains(got, "✅") {
		t.Fatalf("an empty reply must not read as success: %q", got)
	}
	if !strings.Contains(got, "비어") {
		t.Fatalf("summary should say the reply was empty: %q", got)
	}
}
