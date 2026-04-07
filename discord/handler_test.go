package discord

import (
	"testing"
)

func TestSplitMessageShort(t *testing.T) {
	chunks := splitMessage("hello", 2000)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "hello" {
		t.Fatalf("expected 'hello', got '%s'", chunks[0])
	}
}

func TestSplitMessageLong(t *testing.T) {
	// Create a 5000 char string
	msg := ""
	for i := 0; i < 250; i++ {
		msg += "12345678901234567890\n" // 21 chars per line
	}

	chunks := splitMessage(msg, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// Each chunk should be <= 2000 chars (except code block closers)
	for i, chunk := range chunks {
		if len(chunk) > 2010 { // small margin for code block closers
			t.Fatalf("chunk %d too long: %d chars", i, len(chunk))
		}
	}
}

func TestSplitMessageCodeBlock(t *testing.T) {
	msg := "before\n```python\n"
	for i := 0; i < 100; i++ {
		msg += "print('hello world')\n"
	}
	msg += "```\nafter"

	chunks := splitMessage(msg, 500)

	// Verify code blocks are properly closed and reopened
	for i, chunk := range chunks {
		opens := countOccurrences(chunk, "```")
		if opens%2 != 0 {
			// Unclosed code block in a middle chunk is acceptable
			// as long as it's closed by the split logic
			if i < len(chunks)-1 {
				// Middle chunk should end with ```
				if chunk[len(chunk)-3:] != "```" {
					t.Fatalf("chunk %d has unclosed code block without closer", i)
				}
			}
		}
	}
}

func TestSplitMessageNewlineBoundary(t *testing.T) {
	msg := ""
	for i := 0; i < 100; i++ {
		msg += "line content here\n"
	}

	chunks := splitMessage(msg, 200)

	// Should split at newlines, not mid-line
	for _, chunk := range chunks {
		if len(chunk) > 0 && chunk[len(chunk)-1] != '\n' && len(chunk) >= 200 {
			t.Fatal("split happened mid-line")
		}
	}
}

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}
