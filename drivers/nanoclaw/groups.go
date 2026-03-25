package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

// GroupExportMsg is the ndjson message emitted for each group.
type GroupExportMsg struct {
	Type   string          `json:"type"`
	Slug   string          `json:"slug"`
	Config GroupConfig     `json:"config"`
	Files  []BundleFile    `json:"files"`
}

// GroupConfig is the normalized group registration.
type GroupConfig struct {
	Slug            string          `json:"slug"`
	Name            string          `json:"name"`
	JID             string          `json:"jid"`
	Trigger         string          `json:"trigger"`
	AgentName       *string         `json:"agent_name"`
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
func readGroups(sourceDir string) ([]GroupExportMsg, error) {
	rows, err := readGroupRows(sourceDir)
	if err != nil {
		return nil, err
	}

	groupsDir := filepath.Join(sourceDir, "groups")
	var msgs []GroupExportMsg

	for _, row := range rows {
		config := GroupConfig{
			Slug:            row.Folder,
			Name:            row.Name,
			JID:             row.JID,
			Trigger:         row.TriggerPattern,
			AgentName:       row.AgentName,
			RequiresTrigger: row.RequiresTrigger,
			IsMain:          row.IsMain,
		}

		// Preserve NanoClaw-specific fields
		archData := map[string]interface{}{
			"is_default_dm": row.IsDefaultDM,
		}
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
			files, _ = walkGroupDir(groupDir)
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
	if files, err := walkGroupDir(globalDir); err == nil {
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

	return msgs, nil
}

// walkGroupDir returns all files in a group directory as BundleFiles.
// Skips logs/, .git/, and binary files above 10MB.
func walkGroupDir(dir string) ([]BundleFile, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}

	var files []BundleFile
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
		if err != nil || info.Size() > 10*1024*1024 {
			return nil // skip files > 10MB
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		files = append(files, BundleFile{Path: rel, Content: content})
		return nil
	})
	return files, err
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
