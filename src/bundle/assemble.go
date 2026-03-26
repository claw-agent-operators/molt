// SPDX-License-Identifier: AGPL-3.0-or-later
package bundle

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// Assembler builds a Bundle from a stream of driver messages.
type Assembler struct {
	b            *Bundle
	arch         string
	warnings     []string
	sessionCount int
}

// NewAssembler creates an Assembler for a given source arch.
func NewAssembler(sourceArch, sourceVersion string) *Assembler {
	return &Assembler{
		b:    New(sourceArch, sourceVersion),
		arch: sourceArch,
	}
}

// Feed processes one parsed driver message and adds its contents to the bundle.
// Returns (done, error) — done=true when export_complete is received.
func (a *Assembler) Feed(msg map[string]interface{}) (done bool, err error) {
	msgType, _ := msg["type"].(string)

	switch msgType {
	case "group":
		return false, a.addGroup(msg)

	case "task_list":
		return false, a.addTasks(msg)

	case "secrets_keys":
		return false, a.addSecretsTemplate(msg)

	case "skill_manifest":
		return false, a.addSkillManifest(msg)

	case "skill":
		return false, a.addSkill(msg)

	case "session":
		return false, a.addSession(msg)

	case "export_complete":
		rawWarnings, _ := msg["warnings"].([]interface{})
		for _, w := range rawWarnings {
			if s, ok := w.(string); ok {
				a.warnings = append(a.warnings, s)
			}
		}
		a.finalize()
		return true, nil

	case "error":
		code, _ := msg["code"].(string)
		message, _ := msg["message"].(string)
		return false, fmt.Errorf("driver error [%s]: %s", code, message)

	case "progress":
		// informational only — log to stderr
		message, _ := msg["message"].(string)
		fmt.Printf("  %s\n", message)
		return false, nil

	default:
		// unknown message types are ignored (forward-compat)
		return false, nil
	}
}

// Bundle returns the assembled bundle. Call after Feed returns done=true.
func (a *Assembler) Bundle() *Bundle {
	return a.b
}

// addGroup adds a group's config and files to the bundle.
func (a *Assembler) addGroup(msg map[string]interface{}) error {
	slug, _ := msg["slug"].(string)
	if slug == "" {
		return fmt.Errorf("group message missing slug")
	}

	// config.json
	configRaw := msg["config"]
	configJSON, err := json.MarshalIndent(configRaw, "", "  ")
	if err != nil {
		return err
	}
	a.b.Files[filepath.Join("groups", slug, "config.json")] = configJSON
	a.b.Manifest.Groups = append(a.b.Manifest.Groups, slug)

	// group files
	files, _ := msg["files"].([]interface{})
	for _, f := range files {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		relPath, _ := fm["path"].(string)
		if relPath == "" {
			continue
		}

		// content is base64-encoded bytes in JSON
		var content []byte
		switch v := fm["content"].(type) {
		case string:
			// base64 string from JSON marshal of []byte
			decoded, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				a.warnings = append(a.warnings, fmt.Sprintf(
					"groups/%s/%s: skipped (invalid base64)", slug, relPath))
				continue
			}
			content = decoded
		case []interface{}:
			// shouldn't happen but handle gracefully
			continue
		}

		bundlePath := filepath.Join("groups", slug, relPath)
		a.b.Files[bundlePath] = content
	}
	return nil
}

// addTasks adds tasks.json to the bundle.
func (a *Assembler) addTasks(msg map[string]interface{}) error {
	tasks := msg["tasks"]
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	a.b.Files["tasks.json"] = data
	return nil
}

// addSecretsTemplate generates secrets-template.env.
func (a *Assembler) addSecretsTemplate(msg map[string]interface{}) error {
	rawKeys, _ := msg["keys"].([]interface{})
	var keys []string
	for _, k := range rawKeys {
		if s, ok := k.(string); ok {
			keys = append(keys, s)
		}
	}
	a.b.Files["secrets-template.env"] = a.b.SecretsTemplate(keys, a.arch)
	return nil
}

// addSession adds best-effort session files.
func (a *Assembler) addSession(msg map[string]interface{}) error {
	slug, _ := msg["slug"].(string)
	if slug == "" {
		return nil
	}
	files, _ := msg["files"].([]interface{})
	for _, f := range files {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		relPath, _ := fm["path"].(string)
		contentStr, _ := fm["content"].(string)
		if relPath == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(contentStr)
		if err != nil {
			a.warnings = append(a.warnings, fmt.Sprintf(
				"sessions/%s/%s: skipped (invalid base64)", slug, relPath))
			continue
		}
		bundlePath := filepath.Join("sessions", slug, relPath)
		a.b.Files[bundlePath] = decoded
	}
	a.sessionCount++
	return nil
}

// addSkillManifest stores the skill-name → group-slugs mapping in the manifest.
func (a *Assembler) addSkillManifest(msg map[string]interface{}) error {
	raw, _ := msg["skills"].(map[string]interface{})
	if len(raw) == 0 {
		return nil
	}
	if a.b.Manifest.Skills == nil {
		a.b.Manifest.Skills = map[string][]string{}
	}
	for name, slugsRaw := range raw {
		slugs, _ := slugsRaw.([]interface{})
		for _, s := range slugs {
			if gs, ok := s.(string); ok {
				a.b.Manifest.Skills[name] = append(a.b.Manifest.Skills[name], gs)
			}
		}
	}
	return nil
}

// addSkill adds a skill's files to the bundle under skills/<name>/.
func (a *Assembler) addSkill(msg map[string]interface{}) error {
	name, _ := msg["name"].(string)
	if name == "" {
		return nil
	}
	files, _ := msg["files"].([]interface{})
	for _, f := range files {
		fm, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		relPath, _ := fm["path"].(string)
		if relPath == "" {
			continue
		}
		contentStr, _ := fm["content"].(string)
		decoded, err := base64.StdEncoding.DecodeString(contentStr)
		if err != nil {
			a.warnings = append(a.warnings, fmt.Sprintf(
				"skills/%s/%s: skipped (invalid base64)", name, relPath))
			continue
		}
		bundlePath := filepath.Join("skills", name, relPath)
		a.b.Files[bundlePath] = decoded
	}
	return nil
}

// finalize updates the manifest with final state.
func (a *Assembler) finalize() {
	if a.sessionCount > 0 {
		a.warnings = append(a.warnings, fmt.Sprintf(
			"%d session(s) exported best-effort — session IDs may not be valid in target arch",
			a.sessionCount,
		))
	}
	a.b.Manifest.Warnings = a.warnings
	// Update manifest file
	data, _ := json.MarshalIndent(a.b.Manifest, "", "  ")
	a.b.Files["manifest.json"] = data
}

// MarkImported updates the manifest with import metadata.
func (b *Bundle) MarkImported(targetArch, targetVersion string) {
	now := time.Now().UTC().Format(time.RFC3339)
	b.Manifest.ImportedTo = &ArchInfo{
		Arch:        targetArch,
		ArchVersion: targetVersion,
		ImportedAt:  now,
	}
	data, _ := json.MarshalIndent(b.Manifest, "", "  ")
	b.Files["manifest.json"] = data
}
