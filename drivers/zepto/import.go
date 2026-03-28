// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// importBundle is the driver-side representation of a bundle received via JSON.
type importBundle struct {
	Manifest struct {
		Groups []string `json:"groups"`
	} `json:"manifest"`
	Files map[string]string `json:"files"` // path → base64 content
}

// doImport implements the import protocol for the ZeptoClaw driver.
func doImport(destDir string, bundleRaw interface{}, renames map[string]string) {
	// Re-marshal bundleRaw into our typed importBundle.
	data, err := json.Marshal(bundleRaw)
	if err != nil {
		writeError("BUNDLE_PARSE", fmt.Sprintf("failed to marshal bundle: %v", err))
		return
	}
	var b importBundle
	if err := json.Unmarshal(data, &b); err != nil {
		writeError("BUNDLE_PARSE", fmt.Sprintf("failed to parse bundle: %v", err))
		return
	}
	if len(b.Manifest.Groups) == 0 {
		writeError("BUNDLE_EMPTY", "bundle contains no groups")
		return
	}

	dataDir := findDataDir(destDir)
	groupsDir := filepath.Join(dataDir, "groups")
	if err := os.MkdirAll(groupsDir, 0o755); err != nil {
		writeError("FS_ERROR", fmt.Sprintf("failed to create groups dir: %v", err))
		return
	}

	var warnings []string
	var createdPaths []string

	// Import groups
	for _, slug := range b.Manifest.Groups {
		destSlug := slug
		if newName, ok := renames[slug]; ok {
			destSlug = newName
		}

		destGroupDir := filepath.Join(groupsDir, destSlug)

		// Collision check
		if _, err := os.Stat(destGroupDir); err == nil {
			write(map[string]interface{}{
				"type": "collision",
				"slug": destSlug,
			})
			// Clean up already created paths
			for _, p := range createdPaths {
				_ = os.RemoveAll(p)
			}
			return
		}

		if err := os.MkdirAll(destGroupDir, 0o755); err != nil {
			writeError("FS_ERROR", fmt.Sprintf("failed to create group dir %s: %v", destSlug, err))
			for _, p := range createdPaths {
				_ = os.RemoveAll(p)
			}
			return
		}
		createdPaths = append(createdPaths, destGroupDir)

		// Write group files
		prefix := fmt.Sprintf("groups/%s/", slug)
		for bundlePath, b64Content := range b.Files {
			if !strings.HasPrefix(bundlePath, prefix) {
				continue
			}
			relPath := strings.TrimPrefix(bundlePath, prefix)
			if relPath == "config.json" {
				continue // config is handled via channels.json
			}

			content, err := base64.StdEncoding.DecodeString(b64Content)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: invalid base64", bundlePath))
				continue
			}

			destPath := filepath.Join(destGroupDir, relPath)
			if dir := filepath.Dir(destPath); dir != destGroupDir {
				_ = os.MkdirAll(dir, 0o755)
			}
			if err := os.WriteFile(destPath, content, 0o644); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: write error: %v", bundlePath, err))
			}
		}

		write(map[string]interface{}{
			"type":    "progress",
			"message": fmt.Sprintf("imported group: %s", destSlug),
		})
	}

	// Update channels.json with imported group configs
	updateChannelsJSON(dataDir, b, renames, &warnings)

	// Import tasks (best-effort)
	tasksData := b.Files["tasks.json"]
	if tasksData != "" {
		content, err := base64.StdEncoding.DecodeString(tasksData)
		if err == nil {
			var tasks []interface{}
			if json.Unmarshal(content, &tasks) == nil {
				importTasks(destDir, tasks, renames, &warnings)
			}
		}
	}

	// Import sessions (best-effort)
	importSessions(destDir, b.Files, renames, &warnings)

	write(map[string]interface{}{
		"type":     "import_complete",
		"warnings": warnings,
	})
}

// updateChannelsJSON reads the existing channels.json (if any), adds imported
// group configs, and writes back.
func updateChannelsJSON(dataDir string, b importBundle, renames map[string]string, warnings *[]string) {
	channelsPath := filepath.Join(dataDir, "channels.json")

	// Read existing channels
	var channels []map[string]interface{}
	if data, err := os.ReadFile(channelsPath); err == nil {
		_ = json.Unmarshal(data, &channels)
	}

	existingSlugs := map[string]bool{}
	for _, ch := range channels {
		if slug, _ := ch["slug"].(string); slug != "" {
			existingSlugs[slug] = true
		}
	}

	// Add imported groups
	for _, slug := range b.Manifest.Groups {
		destSlug := slug
		if newName, ok := renames[slug]; ok {
			destSlug = newName
		}
		if existingSlugs[destSlug] || slug == "global" {
			continue
		}

		// Read config from bundle
		configKey := fmt.Sprintf("groups/%s/config.json", slug)
		if b64, ok := b.Files[configKey]; ok {
			content, err := base64.StdEncoding.DecodeString(b64)
			if err == nil {
				var cfg map[string]interface{}
				if json.Unmarshal(content, &cfg) == nil {
					cfg["slug"] = destSlug
					channels = append(channels, cfg)
					continue
				}
			}
		}

		// Minimal config if no config.json in bundle
		channels = append(channels, map[string]interface{}{
			"slug": destSlug,
			"name": destSlug,
		})
	}

	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("failed to marshal channels.json: %v", err))
		return
	}
	if err := os.WriteFile(channelsPath, data, 0o644); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("failed to write channels.json: %v", err))
	}
}
