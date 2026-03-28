// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// openDB opens the NanoClaw messages.db at sourceDir/store/messages.db (read-only).
func openDB(sourceDir string) (*sql.DB, error) {
	dbPath := filepath.Join(sourceDir, "store", "messages.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("messages.db not found at %s", dbPath)
	}
	return sql.Open("sqlite", dbPath+"?mode=ro")
}

// openDBRW opens the NanoClaw messages.db at destDir/store/messages.db (read-write).
func openDBRW(destDir string) (*sql.DB, error) {
	dbPath := filepath.Join(destDir, "store", "messages.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("messages.db not found at %s", dbPath)
	}
	return sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
}

// detectArchVersion reads the NanoClaw version from package.json in sourceDir.
func detectArchVersion(sourceDir string) string {
	data, err := os.ReadFile(filepath.Join(sourceDir, "package.json"))
	if err != nil {
		return "unknown"
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) == nil && pkg.Version != "" {
		return pkg.Version
	}
	return "unknown"
}

// probeNanoClaw returns confidence (0.0-1.0) that sourceDir is a NanoClaw install.
func probeNanoClaw(sourceDir string) float64 {
	checks := []struct {
		path   string
		weight float64
	}{
		{filepath.Join(sourceDir, "store", "messages.db"), 0.6},
		{filepath.Join(sourceDir, "groups"), 0.2},
		{filepath.Join(sourceDir, "package.json"), 0.1},
		{filepath.Join(sourceDir, "data", "sessions"), 0.1},
	}
	var score float64
	for _, c := range checks {
		if _, err := os.Stat(c.path); err == nil {
			score += c.weight
		}
	}
	return score
}

// validateSource checks that sourceDir looks like a NanoClaw installation.
func validateSource(sourceDir string) error {
	if _, err := os.Stat(sourceDir); err != nil {
		return fmt.Errorf("directory not found: %s", sourceDir)
	}
	if probeNanoClaw(sourceDir) < 0.5 {
		return fmt.Errorf("%s does not appear to be a NanoClaw installation (missing store/messages.db)", sourceDir)
	}
	return nil
}

// GroupRow represents a row from registered_groups.
type GroupRow struct {
	JID             string
	Name            string
	Folder          string
	TriggerPattern  string
	RequiresTrigger bool
	IsMain          bool
	ContainerConfig *string
}

// readGroupRows reads all registered groups from the DB.
func readGroupRows(sourceDir string) ([]GroupRow, error) {
	db, err := openDB(sourceDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT jid, name, folder, trigger_pattern,
		       requires_trigger, is_main, container_config
		FROM registered_groups
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []GroupRow
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(
			&g.JID, &g.Name, &g.Folder, &g.TriggerPattern,
			&g.RequiresTrigger, &g.IsMain, &g.ContainerConfig,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// TaskRow represents a row from scheduled_tasks.
type TaskRow struct {
	ID             string
	GroupFolder    string
	Prompt         string
	ScheduleType   string
	ScheduleValue  string
	ContextMode    string
	Active         bool
	CreatedAt      string
	TargetGroupJID *string
}

// readTaskRows reads all scheduled tasks from the DB.
func readTaskRows(sourceDir string) ([]TaskRow, error) {
	db, err := openDB(sourceDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT id, group_folder, prompt, schedule_type, schedule_value,
		       context_mode, active, created_at, target_group_jid
		FROM scheduled_tasks
		ORDER BY created_at
	`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("scheduled_tasks query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tasks []TaskRow
	for rows.Next() {
		var t TaskRow
		if err := rows.Scan(
			&t.ID, &t.GroupFolder, &t.Prompt, &t.ScheduleType, &t.ScheduleValue,
			&t.ContextMode, &t.Active, &t.CreatedAt, &t.TargetGroupJID,
		); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// readSecretKeys returns known secret key names from .env (keys only, no values).
func readSecretKeys(sourceDir string) []string {
	// Known NanoClaw secret keys
	known := []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_AUTH_TOKEN",
		"OLLAMA_HOST",
		"GITHUB_TOKEN",
		"SIGNAL_ACCOUNT",
		"SIGNAL_SOCKET_PATH",
	}

	// Check which are actually set in .env
	envPath := filepath.Join(sourceDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		return known // fall back to full list
	}

	var present []string
	for _, line := range splitLines(string(data)) {
		for _, k := range known {
			if len(line) > len(k)+1 && line[:len(k)] == k && line[len(k)] == '=' {
				present = append(present, k)
			}
		}
	}
	if len(present) == 0 {
		return known
	}
	return present
}
