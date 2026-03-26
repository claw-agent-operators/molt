package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// importBundle is the driver-side representation of a bundle received via JSON.
// Bundle.Files values are base64-encoded because json.Marshal([]byte) → base64 string.
type importBundle struct {
	Manifest struct {
		Groups []string `json:"groups"`
	} `json:"manifest"`
	Files map[string]string `json:"files"` // path → base64 content
}

// importGroupConfig matches the GroupConfig serialized into the bundle.
type importGroupConfig struct {
	Slug            string          `json:"slug"`
	Name            string          `json:"name"`
	JID             string          `json:"jid"`
	Trigger         string          `json:"trigger"`
	AgentName       *string         `json:"agent_name"`
	RequiresTrigger bool            `json:"requires_trigger"`
	IsMain          bool            `json:"is_main"`
	ArchNanoclaw    json.RawMessage `json:"_arch_nanoclaw,omitempty"`
}

// archNanoclawFields holds the NanoClaw-specific fields from _arch_nanoclaw.
type archNanoclawFields struct {
	SymlinkTarget   string          `json:"symlink_target"`
	IsDefaultDM     bool            `json:"is_default_dm"`
	ContainerConfig json.RawMessage `json:"container_config"`
}

// doImport implements the import protocol for the NanoClaw driver.
func doImport(destDir string, bundleRaw interface{}, renames map[string]string) {
	// Re-marshal bundleRaw (map[string]interface{}) into our typed importBundle.
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

	// Open destination DB read-write.
	db, err := openDBRW(destDir)
	if err != nil {
		writeError("DB_ERROR", fmt.Sprintf("failed to open dest DB: %v", err))
		return
	}
	defer db.Close()

	groupsDir := filepath.Join(destDir, "groups")
	if err := os.MkdirAll(groupsDir, 0o755); err != nil {
		writeError("FS_ERROR", fmt.Sprintf("failed to create groups dir: %v", err))
		return
	}

	var warnings []string
	imported := 0

	for _, slug := range b.Manifest.Groups {
		destSlug := slug
		if r, ok := renames[slug]; ok {
			destSlug = r
		}

		// Read config.json for this group.
		cfg, ok := b.readGroupConfig(slug)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: missing or invalid config.json — skipped", slug))
			continue
		}

		// Parse NanoClaw-specific fields.
		var arch archNanoclawFields
		if len(cfg.ArchNanoclaw) > 0 {
			_ = json.Unmarshal(cfg.ArchNanoclaw, &arch)
		}

		destGroupDir := filepath.Join(groupsDir, destSlug)

		// Collision check: filesystem.
		if _, err := os.Lstat(destGroupDir); err == nil {
			write(map[string]interface{}{"type": "collision", "slug": destSlug})
			return
		}

		// Collision check: DB (only for groups with JIDs).
		if cfg.JID != "" {
			var count int
			_ = db.QueryRow(
				`SELECT COUNT(*) FROM registered_groups WHERE folder = ?`, destSlug,
			).Scan(&count)
			if count > 0 {
				write(map[string]interface{}{"type": "collision", "slug": destSlug})
				return
			}
		}

		// Filesystem: symlink or write files.
		if arch.SymlinkTarget != "" {
			destTarget := arch.SymlinkTarget
			if r, ok := renames[arch.SymlinkTarget]; ok {
				destTarget = r
			}
			if err := os.Symlink(destTarget, destGroupDir); err != nil {
				warnings = append(warnings, fmt.Sprintf(
					"%s: symlink → %s failed: %v", destSlug, destTarget, err))
			}
		} else {
			// Write all group files from the bundle.
			fileWarnings := b.writeGroupFiles(slug, destGroupDir)
			warnings = append(warnings, fileWarnings...)
			// Ensure logs dir exists even if bundle had none.
			_ = os.MkdirAll(filepath.Join(destGroupDir, "logs"), 0o755)
		}

		// DB insert for registered groups (those that have a JID).
		// Global group and any future JID-less entries skip this.
		if cfg.JID != "" {
			var containerConfigStr *string
			if len(arch.ContainerConfig) > 0 && string(arch.ContainerConfig) != "null" {
				s := string(arch.ContainerConfig)
				containerConfigStr = &s
			}
			_, err = db.Exec(`
				INSERT INTO registered_groups
					(jid, name, folder, trigger_pattern, agent_name,
					 requires_trigger, is_main, is_default_dm, container_config)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
				cfg.JID, cfg.Name, destSlug, cfg.Trigger, cfg.AgentName,
				cfg.RequiresTrigger, cfg.IsMain, arch.IsDefaultDM, containerConfigStr,
			)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: DB insert failed: %v", destSlug, err))
				continue
			}
		}

		write(map[string]interface{}{
			"type":    "progress",
			"message": fmt.Sprintf("✓ %s (%s)", destSlug, cfg.Name),
		})
		imported++
	}

	// Import scheduled tasks.
	if tasksData, ok := b.fileContent("tasks.json"); ok {
		taskCount := importTasks(db, tasksData, renames, &warnings)
		if taskCount > 0 {
			write(map[string]interface{}{
				"type":    "progress",
				"message": fmt.Sprintf("  ✓ %d task(s) imported", taskCount),
			})
		}
	}

	// Import sessions (best-effort: session IDs may not be valid in target).
	sessionCount := b.importSessions(destDir, renames, &warnings)
	if sessionCount > 0 {
		write(map[string]interface{}{
			"type":    "progress",
			"message": fmt.Sprintf("  ✓ %d session(s) restored (best-effort)", sessionCount),
		})
	}

	write(map[string]interface{}{
		"type":     "import_complete",
		"imported": imported,
		"warnings": warnings,
	})
}

// readGroupConfig extracts and parses a group's config.json from the bundle.
func (b *importBundle) readGroupConfig(slug string) (*importGroupConfig, bool) {
	data, ok := b.fileContent("groups/" + slug + "/config.json")
	if !ok {
		return nil, false
	}
	var cfg importGroupConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false
	}
	return &cfg, true
}

// fileContent decodes a bundle file by path (base64 → []byte).
func (b *importBundle) fileContent(path string) ([]byte, bool) {
	encoded, ok := b.Files[path]
	if !ok {
		return nil, false
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return []byte(encoded), true // fallback: treat as raw text
	}
	return content, true
}

// writeGroupFiles writes all non-config bundle files for a slug into destDir.
func (b *importBundle) writeGroupFiles(slug, destDir string) []string {
	prefix := "groups/" + slug + "/"
	var warnings []string
	for path, encoded := range b.Files {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rel := strings.TrimPrefix(path, prefix)
		if rel == "config.json" {
			continue // metadata — not written to disk
		}
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			content = []byte(encoded) // fallback: raw text
		}
		destPath := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: mkdir failed: %v", rel, err))
			continue
		}
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: write failed: %v", rel, err))
		}
	}
	return warnings
}

// importTasks inserts scheduled tasks from tasks.json into the dest DB.
// Returns the count of successfully inserted tasks.
func importTasks(db *sql.DB, data []byte, renames map[string]string, warnings *[]string) int {
	var tasks []map[string]interface{}
	if err := json.Unmarshal(data, &tasks); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("tasks.json parse failed: %v", err))
		return 0
	}

	// Check if table exists; older installs may not have it.
	var tableExists int
	_ = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='scheduled_tasks'`,
	).Scan(&tableExists)
	if tableExists == 0 {
		if len(tasks) > 0 {
			*warnings = append(*warnings, "scheduled_tasks table not found in dest DB — tasks skipped")
		}
		return 0
	}

	count := 0
	for _, t := range tasks {
		id, _ := t["id"].(string)
		groupSlug, _ := t["group_slug"].(string)
		prompt, _ := t["prompt"].(string)
		scheduleType, _ := t["schedule_type"].(string)
		scheduleValue, _ := t["schedule_value"].(string)
		contextMode, _ := t["context_mode"].(string)
		active, _ := t["active"].(bool)
		createdAt, _ := t["created_at"].(string)
		targetGroupJID, _ := t["target_group_jid"].(string)

		if id == "" || prompt == "" {
			continue
		}

		// Apply renames to group_slug.
		if r, ok := renames[groupSlug]; ok {
			groupSlug = r
		}

		var targetJIDVal interface{}
		if targetGroupJID != "" {
			targetJIDVal = targetGroupJID
		}

		_, err := db.Exec(`
			INSERT OR IGNORE INTO scheduled_tasks
				(id, group_folder, prompt, schedule_type, schedule_value,
				 context_mode, active, created_at, target_group_jid)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, groupSlug, prompt, scheduleType, scheduleValue,
			contextMode, active, createdAt, targetJIDVal)
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("task %s: DB insert failed: %v", id, err))
		} else {
			count++
		}
	}
	return count
}

// importSessions writes session files from the bundle to destDir/data/sessions/<slug>/.
// Session IDs are best-effort — they may not be valid in the target installation.
// Returns the number of distinct session slugs written.
func (b *importBundle) importSessions(destDir string, renames map[string]string, warnings *[]string) int {
	sessionsBase := filepath.Join(destDir, "data", "sessions")
	seen := map[string]bool{}

	for path, encoded := range b.Files {
		if !strings.HasPrefix(path, "sessions/") {
			continue
		}
		// path format: sessions/<slug>/<relpath>
		rest := strings.TrimPrefix(path, "sessions/")
		idx := strings.Index(rest, "/")
		if idx < 0 {
			continue // bare sessions/<slug> with no file — skip
		}
		slug, rel := rest[:idx], rest[idx+1:]
		if slug == "" || rel == "" {
			continue
		}

		// Apply group rename if the session slug matches a renamed group.
		destSlug := slug
		if r, ok := renames[slug]; ok {
			destSlug = r
		}

		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			content = []byte(encoded)
		}

		destPath := filepath.Join(sessionsBase, destSlug, rel)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("session %s/%s: mkdir failed: %v", destSlug, rel, err))
			continue
		}
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("session %s/%s: write failed: %v", destSlug, rel, err))
			continue
		}
		seen[destSlug] = true
	}
	return len(seen)
}
