// SPDX-License-Identifier: AGPL-3.0-or-later
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
		Groups []string            `json:"groups"`
		Skills map[string][]string `json:"skills,omitempty"` // skill name → group slugs
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
// Import is best-effort atomic: groups and DB inserts are wrapped in a single
// transaction. On failure, the transaction is rolled back and any filesystem
// paths created so far are removed. Sessions are imported after commit (best-effort).
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
	defer func() { _ = db.Close() }()

	groupsDir := filepath.Join(destDir, "groups")
	if err := os.MkdirAll(groupsDir, 0o755); err != nil {
		writeError("FS_ERROR", fmt.Sprintf("failed to create groups dir: %v", err))
		return
	}

	tx, err := db.Begin()
	if err != nil {
		writeError("DB_ERROR", fmt.Sprintf("failed to begin transaction: %v", err))
		return
	}

	// createdPaths tracks filesystem paths written so far, for cleanup on failure.
	var createdPaths []string
	cleanup := func() {
		_ = tx.Rollback()
		for _, p := range createdPaths {
			_ = os.RemoveAll(p)
		}
	}

	var warnings []string
	imported := 0

	// pendingSymlinks holds symlink groups deferred to pass 2.
	var pendingSymlinks []struct {
		destSlug string
		cfgName  string
		arch     archNanoclawFields
	}

	// Pass 1: process real (non-symlink) groups — filesystem writes + DB inserts.
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

		// Defer symlink groups to pass 2 (their targets may not exist yet).
		if arch.SymlinkTarget != "" {
			pendingSymlinks = append(pendingSymlinks, struct {
				destSlug string
				cfgName  string
				arch     archNanoclawFields
			}{destSlug, cfg.Name, arch})
			continue
		}

		destGroupDir := filepath.Join(groupsDir, destSlug)

		// Collision check: filesystem.
		if _, err := os.Lstat(destGroupDir); err == nil {
			cleanup()
			write(map[string]interface{}{"type": "collision", "slug": destSlug})
			return
		}

		// Collision check: DB (only for groups with JIDs).
		if cfg.JID != "" {
			var count int
			_ = tx.QueryRow(
				`SELECT COUNT(*) FROM registered_groups WHERE folder = ?`, destSlug,
			).Scan(&count)
			if count > 0 {
				cleanup()
				write(map[string]interface{}{"type": "collision", "slug": destSlug})
				return
			}
		}

		// Write all group files from the bundle.
		fileWarnings := b.writeGroupFiles(slug, destGroupDir)
		warnings = append(warnings, fileWarnings...)
		// Ensure logs dir exists even if bundle had none.
		_ = os.MkdirAll(filepath.Join(destGroupDir, "logs"), 0o755)
		createdPaths = append(createdPaths, destGroupDir)

		// DB insert for registered groups (those that have a JID).
		// Global group and any future JID-less entries skip this.
		if cfg.JID != "" {
			var containerConfigStr *string
			if len(arch.ContainerConfig) > 0 && string(arch.ContainerConfig) != "null" {
				s := string(arch.ContainerConfig)
				containerConfigStr = &s
			}
			if _, err = tx.Exec(`
				INSERT INTO registered_groups
					(jid, name, folder, trigger_pattern, agent_name,
					 requires_trigger, is_main, is_default_dm, container_config)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
				cfg.JID, cfg.Name, destSlug, cfg.Trigger, cfg.AgentName,
				cfg.RequiresTrigger, cfg.IsMain, arch.IsDefaultDM, containerConfigStr,
			); err != nil {
				cleanup()
				writeError("DB_ERROR", fmt.Sprintf("%s: DB insert failed: %v", destSlug, err))
				return
			}
		}

		write(map[string]interface{}{
			"type":    "progress",
			"message": fmt.Sprintf("✓ %s (%s)", destSlug, cfg.Name),
		})
		imported++
	}

	// Pass 2: process symlink groups — all real targets should now exist on disk.
	for _, pending := range pendingSymlinks {
		destTarget := pending.arch.SymlinkTarget
		if r, ok := renames[pending.arch.SymlinkTarget]; ok {
			destTarget = r
		}
		destGroupDir := filepath.Join(groupsDir, pending.destSlug)

		// Validate that the target is a real directory (not another symlink).
		// This catches missing targets and circular chains (A→B, B→A).
		targetPath := filepath.Join(groupsDir, destTarget)
		fi, err := os.Lstat(targetPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"%s: symlink target %q not found — skipped", pending.destSlug, destTarget))
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, fmt.Sprintf(
				"%s: symlink target %q is itself a symlink — skipped to avoid circular chain",
				pending.destSlug, destTarget))
			continue
		}
		if !fi.Mode().IsDir() {
			warnings = append(warnings, fmt.Sprintf(
				"%s: symlink target %q is not a directory — skipped", pending.destSlug, destTarget))
			continue
		}

		if err := os.Symlink(destTarget, destGroupDir); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"%s: symlink → %s failed: %v", pending.destSlug, destTarget, err))
			continue
		}
		createdPaths = append(createdPaths, destGroupDir)

		write(map[string]interface{}{
			"type":    "progress",
			"message": fmt.Sprintf("✓ %s (%s) → %s", pending.destSlug, pending.cfgName, destTarget),
		})
		imported++
	}

	// Import scheduled tasks within the transaction.
	if tasksData, ok := b.fileContent("tasks.json"); ok {
		taskCount := importTasks(tx, tasksData, renames, &warnings)
		if taskCount > 0 {
			write(map[string]interface{}{
				"type":    "progress",
				"message": fmt.Sprintf("  ✓ %d task(s) imported", taskCount),
			})
		}
	}

	// Commit. On failure, roll back DB and remove any filesystem paths created.
	if err := tx.Commit(); err != nil {
		cleanup()
		writeError("DB_ERROR", fmt.Sprintf("transaction commit failed: %v", err))
		return
	}

	// Import sessions (best-effort, post-commit: session IDs may not be valid in target).
	sessionCount := b.importSessions(destDir, renames, &warnings)
	if sessionCount > 0 {
		write(map[string]interface{}{
			"type":    "progress",
			"message": fmt.Sprintf("  ✓ %d session(s) restored (best-effort)", sessionCount),
		})
	}

	// Import skills (best-effort, post-commit).
	skillCount := b.importSkills(destDir, renames, &warnings)
	if skillCount > 0 {
		write(map[string]interface{}{
			"type":    "progress",
			"message": fmt.Sprintf("  ✓ %d skill installation(s) restored (best-effort)", skillCount),
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
		return nil, false
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
			warnings = append(warnings, fmt.Sprintf("%s: skipped (invalid base64)", rel))
			continue
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

// importSkills writes skill files from the bundle to each target group's session path.
// Runs post-commit, best-effort: a failed skill install is a warning, not an error.
// Returns the number of (skill, group) pairs successfully installed.
func (b *importBundle) importSkills(destDir string, renames map[string]string, warnings *[]string) int {
	if len(b.Manifest.Skills) == 0 {
		return 0
	}
	count := 0
	for skillName, groupSlugs := range b.Manifest.Skills {
		prefix := "skills/" + skillName + "/"
		for _, slug := range groupSlugs {
			destSlug := slug
			if r, ok := renames[slug]; ok {
				destSlug = r
			}

			destSkillDir := filepath.Join(destDir, "data", "sessions", destSlug, ".claude", "skills", skillName)
			metaPath := filepath.Join(destSkillDir, "_meta.json")

			// Collision: skill dir already has _meta.json.
			if existingMeta, err := os.ReadFile(metaPath); err == nil {
				bundleMetaPath := prefix + "_meta.json"
				if bundleMeta, ok := b.fileContent(bundleMetaPath); ok {
					existingVer := bundleSkillVersion(existingMeta)
					bundleVer := bundleSkillVersion(bundleMeta)
					if existingVer != bundleVer {
						*warnings = append(*warnings, fmt.Sprintf(
							"skill %s/%s: dest has version %q, bundle has %q — skipped (dest may be newer)",
							destSlug, skillName, existingVer, bundleVer,
						))
					}
					// Same version: skip silently.
				} else {
					// Skill dir exists but unknown state — skip to be safe.
					*warnings = append(*warnings, fmt.Sprintf(
						"skill %s/%s: already exists in dest — skipped", destSlug, skillName,
					))
				}
				continue
			} else if !os.IsNotExist(err) {
				// Dir exists but _meta.json is not readable for another reason.
				*warnings = append(*warnings, fmt.Sprintf(
					"skill %s/%s: could not check dest: %v — skipped", destSlug, skillName, err,
				))
				continue
			}

			if err := os.MkdirAll(destSkillDir, 0o755); err != nil {
				*warnings = append(*warnings, fmt.Sprintf(
					"skill %s/%s: mkdir failed: %v", destSlug, skillName, err,
				))
				continue
			}

			wrote := false
			hasMeta := false
			for path, encoded := range b.Files {
				if !strings.HasPrefix(path, prefix) {
					continue
				}
				rel := strings.TrimPrefix(path, prefix)
				content, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					*warnings = append(*warnings, fmt.Sprintf(
						"skill %s/%s: skipped (invalid base64)", skillName, rel,
					))
					continue
				}
				destPath := filepath.Join(destSkillDir, rel)
				if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
					*warnings = append(*warnings, fmt.Sprintf(
						"skill %s/%s: mkdir failed: %v", skillName, rel, err,
					))
					continue
				}
				if err := os.WriteFile(destPath, content, 0o644); err != nil {
					*warnings = append(*warnings, fmt.Sprintf(
						"skill %s/%s: write failed: %v", skillName, rel, err,
					))
					continue
				}
				if rel == "_meta.json" {
					hasMeta = true
				}
				wrote = true
			}

			if wrote {
				// Synthesize minimal _meta.json if the bundle didn't include one.
				if !hasMeta {
					meta := fmt.Sprintf(`{"slug":%q,"version":"unknown","migratedBy":"molt"}`, skillName)
					_ = os.WriteFile(metaPath, []byte(meta), 0o644)
				}
				count++
			}
		}
	}
	return count
}

// bundleSkillVersion reads the "version" field from _meta.json bytes.
// Returns "" if absent or unparseable.
func bundleSkillVersion(data []byte) string {
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	return m.Version
}

// importTasks inserts scheduled tasks from tasks.json into the dest DB transaction.
// Returns the count of successfully inserted tasks.
func importTasks(tx *sql.Tx, data []byte, renames map[string]string, warnings *[]string) int {
	var tasks []map[string]interface{}
	if err := json.Unmarshal(data, &tasks); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("tasks.json parse failed: %v", err))
		return 0
	}

	// Check if table exists; older installs may not have it.
	var tableExists int
	_ = tx.QueryRow(
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

		_, err := tx.Exec(`
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
			*warnings = append(*warnings, fmt.Sprintf("session %s/%s: skipped (invalid base64)", destSlug, rel))
			continue
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
