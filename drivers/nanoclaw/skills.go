// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// TODO(v0.3.0): also scan .claude/skills/ at sourceDir root (global/draft skills).

// discoverUserSkills scans data/sessions/*/.claude/skills/*/ for user-installed skills.
// A skill is considered user-installed if its directory contains _meta.json.
// Returns:
//   - skillFiles: skill name → files to bundle
//   - manifest:   skill name → group slugs that have the skill
//   - warnings:   version-conflict or read errors (non-fatal)
func discoverUserSkills(sourceDir string) (
	skillFiles map[string][]BundleFile,
	manifest map[string][]string,
	warnings []string,
) {
	skillFiles = map[string][]BundleFile{}
	manifest = map[string][]string{}

	sessionsDir := filepath.Join(sourceDir, "data", "sessions")
	if _, err := os.Stat(sessionsDir); err != nil {
		return // no sessions dir
	}

	groupEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("skills: could not read sessions dir: %v", err))
		return
	}

	// seenVersions tracks the version of the first canonical copy of each skill.
	seenVersions := map[string]string{}

	for _, groupEntry := range groupEntries {
		if !groupEntry.IsDir() {
			continue
		}
		groupSlug := groupEntry.Name()
		skillsDir := filepath.Join(sessionsDir, groupSlug, ".claude", "skills")

		if _, err := os.Stat(skillsDir); err != nil {
			continue // no skills dir for this group
		}

		skillEntries, err := os.ReadDir(skillsDir)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skills/%s: could not read skills dir: %v", groupSlug, err))
			continue
		}

		for _, skillEntry := range skillEntries {
			if !skillEntry.IsDir() {
				continue
			}
			skillName := skillEntry.Name()
			skillDir := filepath.Join(skillsDir, skillName)

			// Gate: only user-installed skills have _meta.json.
			metaPath := filepath.Join(skillDir, "_meta.json")
			metaData, err := os.ReadFile(metaPath)
			if err != nil {
				continue // no _meta.json → built-in, skip
			}

			version := readSkillVersion(metaData)

			// Dedup: same name + same version → one canonical copy (first wins).
			// Same name + different version → warn and skip the later one.
			if existing, seen := seenVersions[skillName]; seen {
				if existing != version {
					warnings = append(warnings, fmt.Sprintf(
						"skill %q: version conflict (%q in %s vs %q already seen) — skipping %s copy",
						skillName, version, groupSlug, existing, groupSlug,
					))
				}
				// Either way: record this group as having the skill, but don't re-collect files.
				manifest[skillName] = append(manifest[skillName], groupSlug)
				continue
			}

			// First time seeing this skill: collect files.
			files, walkWarnings, walkErr := walkSkillDir(skillDir)
			for _, w := range walkWarnings {
				warnings = append(warnings, fmt.Sprintf("skill %s/%s: %s", skillName, groupSlug, w))
			}
			if walkErr != nil {
				warnings = append(warnings, fmt.Sprintf("skill %s/%s: walk error: %v", skillName, groupSlug, walkErr))
				continue
			}

			seenVersions[skillName] = version
			skillFiles[skillName] = files
			manifest[skillName] = append(manifest[skillName], groupSlug)
		}
	}
	return
}

// readSkillVersion extracts the "version" string from _meta.json.
// Returns "" if absent or unparseable.
func readSkillVersion(data []byte) string {
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	return m.Version
}

// walkSkillDir reads all files in a skill directory.
// No size limit — skills are expected to be small.
func walkSkillDir(dir string) ([]BundleFile, []string, error) {
	var files []BundleFile
	var warnings []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		content, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: read failed: %v", rel, err))
			return nil
		}
		files = append(files, BundleFile{Path: rel, Content: content})
		return nil
	})
	return files, warnings, err
}

// exportSkills discovers user-installed skills and emits skill_manifest + skill messages.
// Returns the count of distinct skills exported and any warnings.
func exportSkills(sourceDir string) (int, []string) {
	skillFiles, manifest, warnings := discoverUserSkills(sourceDir)
	if len(skillFiles) == 0 {
		return 0, warnings
	}

	// Emit skill_manifest first so the importer knows group assignments.
	write(map[string]interface{}{
		"type":   "skill_manifest",
		"skills": manifest,
	})

	// Emit one skill message per unique skill.
	for name, files := range skillFiles {
		write(map[string]interface{}{
			"type":  "skill",
			"name":  name,
			"files": files,
		})
	}

	return len(skillFiles), warnings
}
