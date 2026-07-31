package update

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
		why             string
	}{
		{"0.0.43", "0.0.42", true, "patch bump"},
		{"0.1.0", "0.0.99", true, "minor beats a high patch"},
		{"1.0.0", "0.9.9", true, "major beats a high minor"},
		{"0.0.42", "0.0.42", false, "same version"},
		{"0.0.41", "0.0.42", false, "remote is behind — never downgrade"},
		{"v0.0.43", "0.0.42", true, "leading v is tolerated"},
		{"0.0.43", "dev", false, "a local build is ahead of the last release, not behind"},
		{"0.0.43", "", false, "unknown current version must not trigger an upgrade"},
		{"0.0.44-rc1", "0.0.43", true, "pre-release suffix still compares by number"},
		{"0.0.10", "0.0.9", true, "numeric, not lexicographic (the classic bug)"},
	}
	for _, tc := range tests {
		if got := IsNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v — %s", tc.latest, tc.current, got, tc.want, tc.why)
		}
	}
}

func TestParseVersionShortAndMalformed(t *testing.T) {
	tests := []struct {
		in   string
		want [3]int
	}{
		{"1.2.3", [3]int{1, 2, 3}},
		{"1.2", [3]int{1, 2, 0}},
		{"1", [3]int{1, 0, 0}},
		{"", [3]int{0, 0, 0}},
		{"v2.0.1", [3]int{2, 0, 1}},
		{"1.2.3-beta.4", [3]int{1, 2, 3}},
		{"garbage", [3]int{0, 0, 0}},
	}
	for _, tc := range tests {
		if got := parseVersion(tc.in); got != tc.want {
			t.Errorf("parseVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCurrentRoundTrip(t *testing.T) {
	orig := Current()
	defer SetCurrent(orig)

	if Current() != "dev" && orig != Current() {
		t.Fatalf("Current() unstable")
	}
	SetCurrent("0.0.42")
	if got := Current(); got != "0.0.42" {
		t.Fatalf("Current() = %q, want 0.0.42", got)
	}
}
