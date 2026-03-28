// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findDataDir locates the ZeptoClaw data directory.
// Resolution: sourceDir (if looks like zepto) → ZEPTOCLAW_DIR env → ~/.zeptoclaw
func findDataDir(sourceDir string) string {
	if sourceDir != "" {
		if _, err := os.Stat(filepath.Join(sourceDir, "config.json")); err == nil {
			return sourceDir
		}
	}
	if env := os.Getenv("ZEPTOCLAW_DIR"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".zeptoclaw")
}

// probeZeptoClaw returns confidence (0.0–1.0) that sourceDir is a ZeptoClaw installation.
func probeZeptoClaw(sourceDir string) float64 {
	dataDir := findDataDir(sourceDir)

	checks := []struct {
		path   string
		weight float64
	}{
		{filepath.Join(dataDir, "config.json"), 0.5},
		{filepath.Join(dataDir, "sessions"), 0.2},
		{filepath.Join(dataDir, "memory"), 0.1},
		{filepath.Join(dataDir, "cron"), 0.1},
	}

	var score float64
	for _, c := range checks {
		if _, err := os.Stat(c.path); err == nil {
			score += c.weight
		}
	}

	// Binary in PATH
	if _, err := exec.LookPath("zeptoclaw"); err == nil {
		score += 0.1
	}

	return score
}

// detectArchVersion reads the ZeptoClaw version from config.json or the binary.
func detectArchVersion(sourceDir string) string {
	dataDir := findDataDir(sourceDir)

	// Try config.json
	data, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err == nil {
		var cfg struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.Version != "" {
			return cfg.Version
		}
	}

	// Try running zeptoclaw version
	out, err := exec.Command("zeptoclaw", "version").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				return line
			}
		}
	}

	return "unknown"
}

// validateSource checks that sourceDir is a valid ZeptoClaw installation.
func validateSource(sourceDir string) error {
	score := probeZeptoClaw(sourceDir)
	if score < 0.5 {
		return fmt.Errorf("%s does not look like a ZeptoClaw installation (confidence: %.1f)", sourceDir, score)
	}
	return nil
}
