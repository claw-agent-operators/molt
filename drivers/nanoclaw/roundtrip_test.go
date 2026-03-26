package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ── fixtures ──────────────────────────────────────────────────────────────────

const schema = `
CREATE TABLE registered_groups (
	jid TEXT, name TEXT, folder TEXT, trigger_pattern TEXT, agent_name TEXT,
	requires_trigger INTEGER NOT NULL DEFAULT 1,
	is_main INTEGER NOT NULL DEFAULT 0,
	is_default_dm INTEGER NOT NULL DEFAULT 0,
	container_config TEXT
);
CREATE TABLE scheduled_tasks (
	id TEXT PRIMARY KEY, group_folder TEXT, prompt TEXT,
	schedule_type TEXT, schedule_value TEXT, context_mode TEXT,
	active INTEGER NOT NULL DEFAULT 1, created_at TEXT, target_group_jid TEXT
);`

func setupSourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// DB
	storeDir := filepath.Join(dir, "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(storeDir, "messages.db"))
	if err != nil {
		t.Fatalf("open src DB: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	db.Exec(`INSERT INTO registered_groups VALUES
		('main@g.us',  'Main',  'main',  '@Andy', NULL, 0, 1, 0, NULL),
		('other@g.us', 'Other', 'other', '@Andy', NULL, 1, 0, 0, NULL)`)
	db.Exec(`INSERT INTO scheduled_tasks VALUES
		('task-1', 'main', 'Daily check', 'cron', '0 9 * * *', 'group', 1, '2026-01-01T00:00:00Z', NULL)`)

	// Group dirs + files
	for _, slug := range []string{"main", "other"} {
		gDir := filepath.Join(dir, "groups", slug)
		os.MkdirAll(filepath.Join(gDir, "logs"), 0o755)
		os.WriteFile(filepath.Join(gDir, "CLAUDE.md"), []byte("# "+slug+"\n"), 0o644)
	}

	// Global group
	globalDir := filepath.Join(dir, "groups", "global")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "CLAUDE.md"), []byte("# Global\n"), 0o644)

	// Session files
	sessionDir := filepath.Join(dir, "data", "sessions", "main")
	os.MkdirAll(sessionDir, 0o755)
	os.WriteFile(filepath.Join(sessionDir, "session.jsonl"), []byte(`{"role":"user"}`+"\n"), 0o644)

	return dir
}

func setupDestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "store")
	os.MkdirAll(storeDir, 0o755)
	db, err := sql.Open("sqlite", filepath.Join(storeDir, "messages.db"))
	if err != nil {
		t.Fatalf("open dest DB: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create dest schema: %v", err)
	}
	os.MkdirAll(filepath.Join(dir, "groups"), 0o755)
	return dir
}

// assembleBundleRaw simulates what bundle.Assembler produces from export messages.
// Returns a map[string]interface{} suitable for passing directly to doImport.
func assembleBundleRaw(t *testing.T, srcDir string) interface{} {
	t.Helper()

	groups, err := readGroups(srcDir)
	if err != nil {
		t.Fatalf("readGroups: %v", err)
	}
	tasks, _ := readTasks(srcDir)

	files := map[string]string{}
	var slugs []string

	for _, g := range groups {
		cfgJSON, _ := json.Marshal(g.Config)
		key := "groups/" + g.Slug + "/config.json"
		files[key] = base64.StdEncoding.EncodeToString(cfgJSON)
		slugs = append(slugs, g.Slug)
		for _, f := range g.Files {
			files["groups/"+g.Slug+"/"+f.Path] = base64.StdEncoding.EncodeToString(f.Content)
		}
	}

	// sessions
	sessDir := filepath.Join(srcDir, "data", "sessions")
	if entries, err := os.ReadDir(sessDir); err == nil {
		for _, slug := range entries {
			if !slug.IsDir() {
				continue
			}
			sFiles, _ := walkSessionDir(filepath.Join(sessDir, slug.Name()))
			for _, f := range sFiles {
				files["sessions/"+slug.Name()+"/"+f.Path] = base64.StdEncoding.EncodeToString(f.Content)
			}
		}
	}

	if tasks != nil {
		taskJSON, _ := json.Marshal(tasks)
		files["tasks.json"] = base64.StdEncoding.EncodeToString(taskJSON)
	}

	return map[string]interface{}{
		"manifest": map[string]interface{}{"groups": slugs},
		"files":    files,
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestRoundTrip(t *testing.T) {
	srcDir := setupSourceDir(t)
	dstDir := setupDestDir(t)

	bundleRaw := assembleBundleRaw(t, srcDir)
	doImport(dstDir, bundleRaw, nil)

	// Verify registered groups
	rows, err := readGroupRows(dstDir)
	if err != nil {
		t.Fatalf("readGroupRows dest: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 registered groups, got %d", len(rows))
	}
	byFolder := map[string]GroupRow{}
	for _, r := range rows {
		byFolder[r.Folder] = r
	}
	if _, ok := byFolder["main"]; !ok {
		t.Error("main group not found in dest DB")
	}
	if r := byFolder["main"]; !r.IsMain {
		t.Error("main group should have is_main=true")
	}

	// Verify group files on disk
	for _, slug := range []string{"main", "other"} {
		p := filepath.Join(dstDir, "groups", slug, "CLAUDE.md")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("group file missing: %s", p)
		}
	}

	// Verify global group files (no DB entry)
	globalMd := filepath.Join(dstDir, "groups", "global", "CLAUDE.md")
	if _, err := os.Stat(globalMd); err != nil {
		t.Errorf("global CLAUDE.md missing: %v", err)
	}
	globalRows := 0
	for _, r := range rows {
		if r.Folder == "global" {
			globalRows++
		}
	}
	if globalRows != 0 {
		t.Error("global group should not appear in registered_groups")
	}

	// Verify tasks
	tasks, err := readTaskRows(dstDir)
	if err != nil {
		t.Fatalf("readTaskRows dest: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}

	// Verify session files
	sessionFile := filepath.Join(dstDir, "data", "sessions", "main", "session.jsonl")
	if _, err := os.Stat(sessionFile); err != nil {
		t.Errorf("session file not restored: %v", err)
	}
}

func TestRoundTripWithRename(t *testing.T) {
	srcDir := setupSourceDir(t)
	dstDir := setupDestDir(t)

	bundleRaw := assembleBundleRaw(t, srcDir)
	renames := map[string]string{"main": "main-imported"}
	doImport(dstDir, bundleRaw, renames)

	rows, err := readGroupRows(dstDir)
	if err != nil {
		t.Fatalf("readGroupRows: %v", err)
	}
	byFolder := map[string]GroupRow{}
	for _, r := range rows {
		byFolder[r.Folder] = r
	}

	if _, ok := byFolder["main-imported"]; !ok {
		t.Error("renamed group 'main-imported' not in dest DB")
	}
	if _, ok := byFolder["main"]; ok {
		t.Error("original slug 'main' should not appear in dest DB after rename")
	}

	// Renamed group files should be at main-imported/
	p := filepath.Join(dstDir, "groups", "main-imported", "CLAUDE.md")
	if _, err := os.Stat(p); err != nil {
		t.Errorf("renamed group file missing at groups/main-imported/CLAUDE.md: %v", err)
	}

	// Session should follow the rename
	sessionFile := filepath.Join(dstDir, "data", "sessions", "main-imported", "session.jsonl")
	if _, err := os.Stat(sessionFile); err != nil {
		t.Errorf("renamed session not at data/sessions/main-imported/: %v", err)
	}
}

func TestCollisionDetection(t *testing.T) {
	srcDir := setupSourceDir(t)
	dstDir := setupDestDir(t)

	// Pre-create the group dir to trigger a collision
	if err := os.MkdirAll(filepath.Join(dstDir, "groups", "main"), 0o755); err != nil {
		t.Fatal(err)
	}

	bundleRaw := assembleBundleRaw(t, srcDir)

	// Capture stdout to check for collision message
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	doImport(dstDir, bundleRaw, nil)

	w.Close()
	os.Stdout = old
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(output), &msg); err != nil {
		t.Fatalf("expected JSON output, got: %q", output)
	}
	if msg["type"] != "collision" {
		t.Errorf("expected collision message, got type=%q", msg["type"])
	}
	if msg["slug"] != "main" {
		t.Errorf("collision slug should be 'main', got %q", msg["slug"])
	}
}
