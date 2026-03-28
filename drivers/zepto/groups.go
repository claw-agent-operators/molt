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

// MaxGroupFileSize is the max size for a group file to be included in export.
const MaxGroupFileSize = 10 * 1024 * 1024 // 10 MB

// GroupConfig is the normalized group config stored in bundle config.json.
type GroupConfig struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	JID     string `json:"jid,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

// BundleFile is a file in the export bundle.
type BundleFile struct {
	Path    string `json:"path"`
	Content string `json:"content"` // base64-encoded
}

// readGroups reads group data from the ZeptoClaw data directory.
// ZeptoClaw may have a channels.json registry and/or a groups/ directory.
func readGroups(sourceDir string) ([]map[string]interface{}, []string) {
	dataDir := findDataDir(sourceDir)
	var groups []map[string]interface{}
	var warnings []string
	seen := map[string]bool{}

	// Try channels.json for registered groups
	channelsPath := filepath.Join(dataDir, "channels.json")
	if data, err := os.ReadFile(channelsPath); err == nil {
		var channels []struct {
			Slug    string `json:"slug"`
			Name    string `json:"name"`
			JID     string `json:"jid"`
			Trigger string `json:"trigger"`
		}
		if json.Unmarshal(data, &channels) == nil {
			for _, ch := range channels {
				if ch.Slug == "" {
					continue
				}
				seen[ch.Slug] = true
				files, fileWarnings := walkGroupDir(dataDir, ch.Slug)
				warnings = append(warnings, fileWarnings...)
				groups = append(groups, map[string]interface{}{
					"type": "group",
					"slug": ch.Slug,
					"config": GroupConfig{
						Slug:    ch.Slug,
						Name:    ch.Name,
						JID:     ch.JID,
						Trigger: ch.Trigger,
					},
					"files": files,
				})
			}
		} else {
			warnings = append(warnings, "failed to parse channels.json: skipping")
		}
	}

	// Also scan groups/ directory for any groups not in channels.json
	groupsDir := filepath.Join(dataDir, "groups")
	entries, err := os.ReadDir(groupsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			slug := e.Name()
			seen[slug] = true
			files, fileWarnings := walkGroupDir(dataDir, slug)
			warnings = append(warnings, fileWarnings...)
			groups = append(groups, map[string]interface{}{
				"type": "group",
				"slug": slug,
				"config": GroupConfig{
					Slug: slug,
					Name: slug,
				},
				"files": files,
			})
		}
	}

	// Export global memory if present
	globalDir := filepath.Join(dataDir, "memory")
	if info, err := os.Stat(globalDir); err == nil && info.IsDir() {
		files, fileWarnings := walkDir(globalDir, "")
		warnings = append(warnings, fileWarnings...)
		if len(files) > 0 {
			groups = append(groups, map[string]interface{}{
				"type": "group",
				"slug": "global",
				"config": GroupConfig{
					Slug: "global",
					Name: "Global Memory",
				},
				"files": files,
			})
		}
	}

	return groups, warnings
}

// walkGroupDir collects files from groups/<slug>/ directory.
func walkGroupDir(dataDir, slug string) ([]BundleFile, []string) {
	groupDir := filepath.Join(dataDir, "groups", slug)
	return walkDir(groupDir, "")
}

// walkDir recursively collects files, base64-encoding content.
func walkDir(root, prefix string) ([]BundleFile, []string) {
	var files []BundleFile
	var warnings []string

	dir := root
	if prefix != "" {
		dir = filepath.Join(root, prefix)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	for _, e := range entries {
		name := e.Name()
		relPath := name
		if prefix != "" {
			relPath = filepath.Join(prefix, name)
		}

		// Skip common non-essential directories
		if e.IsDir() {
			if name == ".git" || name == "logs" || name == "node_modules" {
				continue
			}
			subFiles, subWarnings := walkDir(root, relPath)
			files = append(files, subFiles...)
			warnings = append(warnings, subWarnings...)
			continue
		}

		// Skip hidden files (except CLAUDE.md and similar)
		if strings.HasPrefix(name, ".") && name != ".env" {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > MaxGroupFileSize {
			warnings = append(warnings, fmt.Sprintf(
				"groups/%s: skipped (%.1f MB > 10 MB limit)", relPath, float64(info.Size())/(1024*1024)))
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("groups/%s: read error: %v", relPath, err))
			continue
		}

		files = append(files, BundleFile{
			Path:    relPath,
			Content: base64.StdEncoding.EncodeToString(content),
		})
	}

	return files, warnings
}
