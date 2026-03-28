// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// MaxGroupFileSize is the maximum file size included in a group export.
// Files exceeding this limit are skipped with a warning.
const MaxGroupFileSize = 10 * 1024 * 1024 // 10 MB

// MaxSessionFileSize is the maximum file size included in a session export.
// Files exceeding this limit are skipped with a warning.
const MaxSessionFileSize = 5 * 1024 * 1024 // 5 MB

// GroupExportMsg is the ndjson message emitted for each group.
type GroupExportMsg struct {
	Type   string       `json:"type"`
	Slug   string       `json:"slug"`
	Config GroupConfig  `json:"config"`
	Files  []BundleFile `json:"files"`
}

// GroupConfig is the normalized group registration.
type GroupConfig struct {
	Slug            string          `json:"slug"`
	Name            string          `json:"name"`
	JID             string          `json:"jid"`
	Trigger         string          `json:"trigger"`
	RequiresTrigger bool            `json:"requires_trigger"`
	IsMain          bool            `json:"is_main"`
	AddedAt         string          `json:"added_at,omitempty"`
	ArchNanoclaw    json.RawMessage `json:"_arch_nanoclaw,omitempty"`
}

// BundleFile is a file entry in the bundle.
type BundleFile struct {
	Path    string `json:"path"`    // relative to group root
	Content []byte `json:"content"` // base64 in JSON
}

// readGroups reads all groups from the DB and their files from disk.
// Returns export messages, any file-level warnings, and an error.
func readGroups(sourceDir string) ([]GroupExportMsg, []string, error) {
	rows, err := readGroupRows(sourceDir)
	if err != nil {
		return nil, nil, err
	}

	groupsDir := filepath.Join(sourceDir, "groups")
	var msgs []GroupExportMsg
	var warnings []string

	for _, row := range rows {
		config := GroupConfig{
			Slug:            row.Folder,
			Name:            row.Name,
			JID:             row.JID,
			Trigger:         row.TriggerPattern,
			RequiresTrigger: row.RequiresTrigger,
			IsMain:          row.IsMain,
		}

		// Preserve NanoClaw-specific fields
		archData := map[string]interface{}{}
		if row.ContainerConfig != nil {
			var cc interface{}
			if json.Unmarshal([]byte(*row.ContainerConfig), &cc) == nil {
				archData["container_config"] = cc
			}
		}
		// Detect symlinked group dirs (e.g. main-signal → main).
		// Export the symlink relationship; don't duplicate files.
		groupDir := filepath.Join(groupsDir, row.Folder)
		var files []BundleFile
		if fi, err := os.Lstat(groupDir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(groupDir); err == nil {
				// Normalize: resolve relative symlink to a slug name
				target = filepath.Base(filepath.Clean(filepath.Join(groupsDir, target)))
				archData["symlink_target"] = target
			}
			// files stays nil — no content to export for symlinked dirs
		} else {
			var walkWarnings []string
			files, walkWarnings, _ = walkGroupDir(groupDir)
			for _, w := range walkWarnings {
				warnings = append(warnings, fmt.Sprintf("%s/%s", row.Folder, w))
			}
		}
		archJSON, _ := json.Marshal(archData)
		config.ArchNanoclaw = archJSON

		msgs = append(msgs, GroupExportMsg{
			Type:   "group",
			Slug:   row.Folder,
			Config: config,
			Files:  files,
		})
	}

	// Add global group if it exists
	globalDir := filepath.Join(groupsDir, "global")
	if files, walkWarnings, err := walkGroupDir(globalDir); err == nil {
		for _, w := range walkWarnings {
			warnings = append(warnings, fmt.Sprintf("global/%s", w))
		}
		msgs = append(msgs, GroupExportMsg{
			Type: "group",
			Slug: "global",
			Config: GroupConfig{
				Slug: "global",
				Name: "Global",
			},
			Files: files,
		})
	}

	return msgs, warnings, nil
}

// walkGroupDir returns all files in a group directory as BundleFiles.
// Skips logs/, .git/, and agent-runner-src/. Files above MaxGroupFileSize are
// skipped with a warning. Returns files, warnings, and any walk error.
func walkGroupDir(dir string) ([]BundleFile, []string, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, nil, err
	}

	var files []BundleFile
	var warnings []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			base := d.Name()
			if base == "logs" || base == ".git" || base == "agent-runner-src" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > MaxGroupFileSize {
			warnings = append(warnings, fmt.Sprintf(
				"%s: skipped (%.1f MB exceeds %d MB limit)",
				rel, float64(info.Size())/(1024*1024), MaxGroupFileSize/(1024*1024),
			))
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		files = append(files, BundleFile{Path: rel, Content: content})
		return nil
	})
	return files, warnings, err
}

// readTasks returns scheduled tasks in normalized bundle format.
func readTasks(sourceDir string) ([]map[string]interface{}, error) {
	rows, err := readTaskRows(sourceDir)
	if err != nil || rows == nil {
		return nil, err
	}

	var tasks []map[string]interface{}
	for _, t := range rows {
		task := map[string]interface{}{
			"id":             t.ID,
			"group_slug":     t.GroupFolder,
			"prompt":         t.Prompt,
			"schedule_type":  t.ScheduleType,
			"schedule_value": t.ScheduleValue,
			"context_mode":   t.ContextMode,
			"active":         t.Active,
			"created_at":     t.CreatedAt,
		}
		if t.TargetGroupJID != nil {
			task["target_group_jid"] = *t.TargetGroupJID
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}
