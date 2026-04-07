package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func checkUpdate() {
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("https://api.github.com/repos/tackish/pigeon-claw/releases/latest")
		if err != nil {
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return
		}

		var release struct {
			TagName string `json:"tag_name"`
		}
		if json.Unmarshal(body, &release) != nil {
			return
		}

		latest := strings.TrimPrefix(release.TagName, "v")
		current := version
		if current == "dev" || current == "" {
			return
		}

		if latest != current && latest > current {
			slog.Warn("new version available",
				"current", current,
				"latest", latest,
				"update", "brew update && brew upgrade pigeon-claw",
			)
			fmt.Printf("\n  ⬆ New version available: %s → %s\n", current, latest)
			fmt.Println("    Run: brew update && brew upgrade pigeon-claw")
			fmt.Println()
		}
	}()
}
