// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readTasks reads scheduled tasks from the cron/ directory.
// ZeptoClaw stores tasks as individual JSON files in cron/.
func readTasks(sourceDir string) ([]interface{}, []string) {
	dataDir := findDataDir(sourceDir)
	cronDir := filepath.Join(dataDir, "cron")
	var warnings []string

	entries, err := os.ReadDir(cronDir)
	if err != nil {
		// cron/ may not exist — not an error
		return []interface{}{}, nil
	}

	var tasks []interface{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cronDir, e.Name()))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cron/%s: read error: %v", e.Name(), err))
			continue
		}
		var task interface{}
		if json.Unmarshal(data, &task) != nil {
			warnings = append(warnings, fmt.Sprintf("cron/%s: invalid JSON", e.Name()))
			continue
		}
		tasks = append(tasks, task)
	}

	return tasks, warnings
}

// readSecretKeys reads known secret key names from config.json.
// Returns key names only, not values.
func readSecretKeys(sourceDir string) []string {
	dataDir := findDataDir(sourceDir)
	data, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return []string{}
	}

	var cfg map[string]interface{}
	if json.Unmarshal(data, &cfg) != nil {
		return []string{}
	}

	// Look for common secret key patterns
	var keys []string
	secretFields := []string{"api_key", "oauth_token", "webhook_secret"}
	for _, field := range secretFields {
		if _, ok := cfg[field]; ok {
			keys = append(keys, field)
		}
	}

	return keys
}

// importTasks writes task files to the cron/ directory.
func importTasks(destDir string, tasks []interface{}, renames map[string]string, warnings *[]string) {
	if len(tasks) == 0 {
		return
	}

	dataDir := findDataDir(destDir)
	cronDir := filepath.Join(dataDir, "cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("failed to create cron dir: %v", err))
		return
	}

	for i, task := range tasks {
		taskMap, ok := task.(map[string]interface{})
		if !ok {
			continue
		}

		// Apply group renames to task's group_folder field
		if gf, ok := taskMap["group_folder"].(string); ok {
			if newName, renamed := renames[gf]; renamed {
				taskMap["group_folder"] = newName
			}
		}

		data, err := json.MarshalIndent(taskMap, "", "  ")
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("task %d: marshal error: %v", i, err))
			continue
		}

		// Use task ID as filename, or generate one
		id, _ := taskMap["id"].(string)
		if id == "" {
			id = fmt.Sprintf("task-%d", i)
		}
		filename := id + ".json"
		destPath := filepath.Join(cronDir, filename)
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("task %s: write error: %v", id, err))
		}
	}
}
