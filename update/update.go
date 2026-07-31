// Package update checks GitHub for newer pigeon-claw releases and applies
// them via Homebrew. It lives outside cmd/ so both the startup check and
// the Discord /update command can share it — discord/ cannot import cmd/
// without a cycle (cmd → bot → discord).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const releasesURL = "https://api.github.com/repos/tackish/pigeon-claw/releases/latest"

var (
	mu      sync.RWMutex
	current = "dev"
)

// SetCurrent records the running version. The build injects it into
// cmd.version via ldflags, so cmd hands it over at startup rather than
// this package owning a second injection point.
func SetCurrent(v string) {
	mu.Lock()
	current = v
	mu.Unlock()
}

// Current returns the running version, "dev" for unreleased builds.
func Current() string {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Latest returns the newest published release version, "v" prefix stripped.
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github returned %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	v := strings.TrimPrefix(release.TagName, "v")
	if v == "" {
		return "", fmt.Errorf("release has no tag name")
	}
	return v, nil
}

// IsNewer reports whether latest is a higher semver than current.
// A "dev" (unreleased) build is never considered outdated — it is
// typically ahead of the last release, not behind it.
func IsNewer(latest, cur string) bool {
	if cur == "dev" || cur == "" {
		return false
	}
	l, c := parseVersion(latest), parseVersion(cur)
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	var parts [3]int
	for i, s := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		if i > 2 {
			break
		}
		// Tolerate suffixes like "1.2.3-rc1" by reading the leading digits.
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		parts[i], _ = strconv.Atoi(s[:end])
	}
	return parts
}

// Upgrade runs `brew update && brew upgrade pigeon-claw`. On failure the
// error carries the command's combined output, which is the only useful
// thing to show a user who can't see the terminal.
func Upgrade(ctx context.Context) error {
	for _, args := range [][]string{
		{"update"},
		{"upgrade", "pigeon-claw"},
	} {
		cmd := exec.CommandContext(ctx, "brew", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			detail := strings.TrimSpace(string(out))
			if len(detail) > 1500 { // keep it inside a Discord message
				detail = detail[len(detail)-1500:]
			}
			return fmt.Errorf("brew %s failed: %w\n%s", strings.Join(args, " "), err, detail)
		}
	}
	return nil
}
