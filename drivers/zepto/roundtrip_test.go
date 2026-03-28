// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupSourceDir creates a minimal ZeptoClaw installation fixture.
func setupSourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// config.json
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":"0.8.2","api_key":"sk-test"}`), 0o644)

	// channels.json
	channels := []map[string]interface{}{
		{"slug": "surf-crew", "name": "Surf Crew", "jid": "123@g.us", "trigger": "@Andy"},
		{"slug": "dev-team", "name": "Dev Team", "jid": "456@g.us", "trigger": "@Bot"},
	}
	data, _ := json.MarshalIndent(channels, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "channels.json"), data, 0o644)

	// groups/
	_ = os.MkdirAll(filepath.Join(dir, "groups", "surf-crew"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "groups", "surf-crew", "CLAUDE.md"), []byte("# Surf Crew\nMemory for surf crew."), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "groups", "dev-team"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "groups", "dev-team", "CLAUDE.md"), []byte("# Dev Team\nMemory for dev team."), 0o644)

	// memory/ (global)
	_ = os.MkdirAll(filepath.Join(dir, "memory"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "memory", "CLAUDE.md"), []byte("# Global Memory"), 0o644)

	// sessions/
	_ = os.MkdirAll(filepath.Join(dir, "sessions"), 0o755)
	session := map[string]interface{}{
		"key":        "cli:cli",
		"updated_at": "2025-01-01T00:00:00Z",
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hi there"},
		},
	}
	sessData, _ := json.MarshalIndent(session, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "sessions", "cli-cli.json"), sessData, 0o644)

	// cron/
	_ = os.MkdirAll(filepath.Join(dir, "cron"), 0o755)
	task := map[string]interface{}{
		"id":             "task-1",
		"group_folder":   "surf-crew",
		"prompt":         "check surf conditions",
		"schedule_type":  "cron",
		"schedule_value": "0 6 * * *",
	}
	taskData, _ := json.MarshalIndent(task, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "cron", "task-1.json"), taskData, 0o644)

	return dir
}

func setupDestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":"0.8.2"}`), 0o644)
	return dir
}

func TestExportGroups(t *testing.T) {
	srcDir := setupSourceDir(t)
	groups, warnings := readGroups(srcDir)

	// Should have surf-crew, dev-team, and global
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups (2 channels + global), got %d", len(groups))
	}

	slugs := map[string]bool{}
	for _, g := range groups {
		slug, _ := g["slug"].(string)
		slugs[slug] = true
	}
	if !slugs["surf-crew"] {
		t.Error("missing surf-crew group")
	}
	if !slugs["dev-team"] {
		t.Error("missing dev-team group")
	}
	if !slugs["global"] {
		t.Error("missing global group")
	}

	if len(warnings) > 0 {
		t.Logf("warnings: %v", warnings)
	}
}

func TestExportGroupFiles(t *testing.T) {
	srcDir := setupSourceDir(t)
	groups, _ := readGroups(srcDir)

	for _, g := range groups {
		slug, _ := g["slug"].(string)
		if slug != "surf-crew" {
			continue
		}
		files, ok := g["files"].([]BundleFile)
		if !ok {
			t.Fatal("surf-crew files not a []BundleFile")
		}
		found := false
		for _, f := range files {
			if f.Path == "CLAUDE.md" {
				found = true
				content, _ := base64.StdEncoding.DecodeString(f.Content)
				if !strings.Contains(string(content), "Surf Crew") {
					t.Error("CLAUDE.md content mismatch")
				}
			}
		}
		if !found {
			t.Error("CLAUDE.md not found in surf-crew files")
		}
	}
}

func TestExportTasks(t *testing.T) {
	srcDir := setupSourceDir(t)
	tasks, warnings := readTasks(srcDir)

	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if len(warnings) > 0 {
		t.Logf("warnings: %v", warnings)
	}

	taskMap, ok := tasks[0].(map[string]interface{})
	if !ok {
		t.Fatal("task is not a map")
	}
	if taskMap["id"] != "task-1" {
		t.Errorf("expected task id task-1, got %v", taskMap["id"])
	}
}

func TestExportSecretKeys(t *testing.T) {
	srcDir := setupSourceDir(t)
	keys := readSecretKeys(srcDir)

	if len(keys) != 1 || keys[0] != "api_key" {
		t.Errorf("expected [api_key], got %v", keys)
	}
}

func TestExportSessions(t *testing.T) {
	srcDir := setupSourceDir(t)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	warnings := exportSessions(srcDir)

	_ = w.Close()
	os.Stdout = old

	buf := make([]byte, 65536)
	n, _ := r.Read(buf)
	_ = r.Close()

	if len(warnings) > 0 {
		t.Logf("warnings: %v", warnings)
	}

	// Parse the emitted session message
	var msg map[string]interface{}
	if err := json.Unmarshal(buf[:n], &msg); err != nil {
		t.Fatalf("failed to parse session message: %v", err)
	}
	if msg["type"] != "session" {
		t.Errorf("expected type=session, got %v", msg["type"])
	}
}

func TestImportRoundTrip(t *testing.T) {
	srcDir := setupSourceDir(t)
	destDir := setupDestDir(t)

	// Build a bundle from export data
	groups, _ := readGroups(srcDir)
	tasks, _ := readTasks(srcDir)

	bundle := map[string]interface{}{
		"manifest": map[string]interface{}{
			"groups": []string{"surf-crew", "dev-team"},
		},
		"files": map[string]string{},
	}
	files := bundle["files"].(map[string]string)

	// Add group files
	for _, g := range groups {
		slug, _ := g["slug"].(string)
		if slug == "global" {
			continue
		}
		cfg, _ := g["config"].(GroupConfig)
		cfgData, _ := json.Marshal(cfg)
		files["groups/"+slug+"/config.json"] = base64.StdEncoding.EncodeToString(cfgData)

		bundleFiles, _ := g["files"].([]BundleFile)
		for _, f := range bundleFiles {
			files["groups/"+slug+"/"+f.Path] = f.Content
		}
	}

	// Add tasks
	tasksData, _ := json.Marshal(tasks)
	files["tasks.json"] = base64.StdEncoding.EncodeToString(tasksData)

	// Capture stdout (for progress messages)
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	doImport(destDir, bundle, nil)

	_ = w.Close()
	os.Stdout = old

	// Verify group directories were created
	dataDir := findDataDir(destDir)
	if _, err := os.Stat(filepath.Join(dataDir, "groups", "surf-crew", "CLAUDE.md")); err != nil {
		t.Error("surf-crew/CLAUDE.md not created")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "groups", "dev-team", "CLAUDE.md")); err != nil {
		t.Error("dev-team/CLAUDE.md not created")
	}

	// Verify channels.json was updated
	channelsData, err := os.ReadFile(filepath.Join(dataDir, "channels.json"))
	if err != nil {
		t.Fatalf("channels.json not created: %v", err)
	}
	var channels []map[string]interface{}
	if err := json.Unmarshal(channelsData, &channels); err != nil {
		t.Fatalf("channels.json parse error: %v", err)
	}
	if len(channels) < 2 {
		t.Errorf("expected ≥2 channels, got %d", len(channels))
	}

	// Verify task was imported
	cronEntries, err := os.ReadDir(filepath.Join(dataDir, "cron"))
	if err != nil {
		t.Fatalf("cron dir not created: %v", err)
	}
	if len(cronEntries) != 1 {
		t.Errorf("expected 1 cron file, got %d", len(cronEntries))
	}
}

func TestImportWithRename(t *testing.T) {
	destDir := setupDestDir(t)

	bundle := map[string]interface{}{
		"manifest": map[string]interface{}{
			"groups": []string{"old-name"},
		},
		"files": map[string]string{
			"groups/old-name/CLAUDE.md": base64.StdEncoding.EncodeToString([]byte("# Renamed")),
		},
	}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	doImport(destDir, bundle, map[string]string{"old-name": "new-name"})

	_ = w.Close()
	os.Stdout = old

	dataDir := findDataDir(destDir)
	if _, err := os.Stat(filepath.Join(dataDir, "groups", "new-name", "CLAUDE.md")); err != nil {
		t.Error("renamed group dir not created")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "groups", "old-name")); err == nil {
		t.Error("old group name should not exist")
	}
}

func TestImportCollision(t *testing.T) {
	destDir := setupDestDir(t)
	dataDir := findDataDir(destDir)

	// Pre-create the group dir
	_ = os.MkdirAll(filepath.Join(dataDir, "groups", "existing"), 0o755)

	bundle := map[string]interface{}{
		"manifest": map[string]interface{}{
			"groups": []string{"existing"},
		},
		"files": map[string]string{},
	}

	// Capture stdout for collision message
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	doImport(destDir, bundle, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	_ = r.Close()

	var msg map[string]interface{}
	if err := json.Unmarshal(buf[:n], &msg); err != nil {
		t.Fatalf("failed to parse collision message: %v", err)
	}
	if msg["type"] != "collision" {
		t.Errorf("expected collision message, got type=%v", msg["type"])
	}
	if msg["slug"] != "existing" {
		t.Errorf("expected collision slug=existing, got %v", msg["slug"])
	}
}

func TestSessionConversion_ZeptoFormat(t *testing.T) {
	// Already in zepto format — should pass through
	input := `{"key":"cli:cli","messages":[{"role":"user","content":"hello"}]}`
	result := convertSessionToZepto([]byte(input), "test.json")

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	msgs, _ := parsed["messages"].([]interface{})
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestSessionConversion_JSONL(t *testing.T) {
	// JSONL format with user/assistant messages + tool events
	input := `{"role":"user","content":"hello"}
{"role":"assistant","content":"hi there"}
{"role":"tool","content":"result"}
{"role":"user","content":"thanks"}`

	result := convertSessionToZepto([]byte(input), "session.jsonl")

	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(parsed.Messages) != 3 {
		t.Errorf("expected 3 messages (user, assistant, user — tool dropped), got %d", len(parsed.Messages))
	}
	if parsed.Messages[0].Role != "user" || parsed.Messages[0].Content != "hello" {
		t.Errorf("first message mismatch: %+v", parsed.Messages[0])
	}
}

func TestSessionConversion_ContentBlocks(t *testing.T) {
	// Nanoclaw-style content blocks
	input := `{"role":"assistant","content":[{"type":"text","text":"I can help with that."},{"type":"tool_use","id":"123"}]}`

	result := convertSessionToZepto([]byte(input), "session.jsonl")

	var parsed struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(parsed.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(parsed.Messages))
	}
	if parsed.Messages[0].Content != "I can help with that." {
		t.Errorf("content mismatch: %q", parsed.Messages[0].Content)
	}
}
