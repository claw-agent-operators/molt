// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kenbolton/molt/src/bundle"
)

// newBundle creates a test bundle with the given groups pre-configured.
func newBundle(groups ...string) *bundle.Bundle {
	b := bundle.New("nanoclaw", "1.0.0")
	b.Manifest.Groups = groups
	return b
}

// setConfig writes a minimal config.json for a group.
func setConfig(b *bundle.Bundle, slug string, fields map[string]interface{}) {
	data, _ := json.Marshal(fields)
	b.Files["groups/"+slug+"/config.json"] = data
}

// setGroupFile sets an arbitrary file inside a group directory.
func setGroupFile(b *bundle.Bundle, slug, name string, content []byte) {
	b.Files["groups/"+slug+"/"+name] = content
}

// setTaskJSON writes a tasks.json to the bundle.
func setTaskJSON(b *bundle.Bundle, tasks []map[string]interface{}) {
	data, _ := json.Marshal(tasks)
	b.Files["tasks.json"] = data
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestComputeDiff_IdenticalBundles(t *testing.T) {
	a := newBundle("main", "dev")
	setConfig(a, "main", map[string]interface{}{"name": "Main"})
	setConfig(a, "dev", map[string]interface{}{"name": "Dev"})

	b := newBundle("main", "dev")
	setConfig(b, "main", map[string]interface{}{"name": "Main"})
	setConfig(b, "dev", map[string]interface{}{"name": "Dev"})

	d := computeDiff(a, b, "", false)
	if d.HasDifferences() {
		t.Errorf("expected no differences, got: added=%v removed=%v changed=%v",
			d.GroupsAdded, d.GroupsRemoved, d.GroupsChanged)
	}
}

func TestComputeDiff_GroupAdded(t *testing.T) {
	a := newBundle("main")
	setConfig(a, "main", map[string]interface{}{"name": "Main"})

	b := newBundle("main", "newteam")
	setConfig(b, "main", map[string]interface{}{"name": "Main"})
	setConfig(b, "newteam", map[string]interface{}{"name": "New Team"})
	setGroupFile(b, "newteam", "CLAUDE.md", []byte("hello"))

	d := computeDiff(a, b, "", false)
	if len(d.GroupsAdded) != 1 || d.GroupsAdded[0] != "newteam" {
		t.Errorf("expected GroupsAdded=[newteam], got %v", d.GroupsAdded)
	}
	if len(d.GroupsRemoved) != 0 {
		t.Errorf("unexpected removals: %v", d.GroupsRemoved)
	}
}

func TestComputeDiff_GroupRemoved(t *testing.T) {
	a := newBundle("main", "oldteam")
	setConfig(a, "main", map[string]interface{}{"name": "Main"})
	setConfig(a, "oldteam", map[string]interface{}{"name": "Old Team"})

	b := newBundle("main")
	setConfig(b, "main", map[string]interface{}{"name": "Main"})

	d := computeDiff(a, b, "", false)
	if len(d.GroupsRemoved) != 1 || d.GroupsRemoved[0] != "oldteam" {
		t.Errorf("expected GroupsRemoved=[oldteam], got %v", d.GroupsRemoved)
	}
	if len(d.GroupsAdded) != 0 {
		t.Errorf("unexpected additions: %v", d.GroupsAdded)
	}
}

func TestComputeDiff_GroupConfigChanged(t *testing.T) {
	a := newBundle("main")
	setConfig(a, "main", map[string]interface{}{"name": "Main"})

	b := newBundle("main")
	setConfig(b, "main", map[string]interface{}{"name": "Main Channel"})

	d := computeDiff(a, b, "", false)
	if len(d.GroupsChanged) != 1 {
		t.Fatalf("expected 1 changed group, got %d", len(d.GroupsChanged))
	}
	gd := d.GroupsChanged[0]
	if gd.Slug != "main" {
		t.Errorf("expected slug=main, got %s", gd.Slug)
	}
	if len(gd.ConfigChanges) != 1 {
		t.Fatalf("expected 1 config change, got %d", len(gd.ConfigChanges))
	}
	c := gd.ConfigChanges[0]
	if c.Field != "name" || c.From != "Main" || c.To != "Main Channel" {
		t.Errorf("unexpected config change: %+v", c)
	}
}

func TestComputeDiff_TaskAdded(t *testing.T) {
	a := newBundle("main")
	b := newBundle("main")
	setTaskJSON(b, []map[string]interface{}{
		{"id": "task-001", "group_slug": "main", "schedule_type": "cron",
			"schedule_value": "0 8 * * *", "prompt": "run report", "active": true},
	})

	d := computeDiff(a, b, "", false)
	if len(d.TasksAdded) != 1 || d.TasksAdded[0].ID != "task-001" {
		t.Errorf("expected task-001 added, got %v", d.TasksAdded)
	}
}

func TestComputeDiff_TaskRemoved(t *testing.T) {
	a := newBundle("main")
	setTaskJSON(a, []map[string]interface{}{
		{"id": "task-001", "group_slug": "main", "schedule_type": "cron",
			"schedule_value": "0 8 * * *", "prompt": "run report", "active": true},
	})
	b := newBundle("main")

	d := computeDiff(a, b, "", false)
	if len(d.TasksRemoved) != 1 || d.TasksRemoved[0].ID != "task-001" {
		t.Errorf("expected task-001 removed, got %v", d.TasksRemoved)
	}
}

func TestComputeDiff_TaskChanged(t *testing.T) {
	task := func(schedVal string) []map[string]interface{} {
		return []map[string]interface{}{
			{"id": "task-001", "group_slug": "main", "schedule_type": "cron",
				"schedule_value": schedVal, "prompt": "run report", "active": true},
		}
	}
	a := newBundle("main")
	setTaskJSON(a, task("0 9 * * *"))
	b := newBundle("main")
	setTaskJSON(b, task("0 10 * * *"))

	d := computeDiff(a, b, "", false)
	if len(d.TasksChanged) != 1 {
		t.Fatalf("expected 1 changed task, got %d", len(d.TasksChanged))
	}
	td := d.TasksChanged[0]
	if td.ID != "task-001" {
		t.Errorf("expected id=task-001, got %s", td.ID)
	}
	if len(td.Changes) != 1 || td.Changes[0].Field != "schedule_value" {
		t.Errorf("unexpected task changes: %+v", td.Changes)
	}
	if td.Changes[0].From != "0 9 * * *" || td.Changes[0].To != "0 10 * * *" {
		t.Errorf("unexpected schedule change: %+v", td.Changes[0])
	}
}

func TestComputeDiff_SkillChanged(t *testing.T) {
	a := newBundle("main")
	a.Files["skills/pdf/prompt.md"] = []byte("old content")
	a.Files["skills/pdf/meta.json"] = []byte("{}")

	b := newBundle("main")
	b.Files["skills/pdf/prompt.md"] = []byte("new content")
	b.Files["skills/pdf/meta.json"] = []byte("{}")
	b.Files["skills/pdf/extra.txt"] = []byte("extra")

	d := computeDiff(a, b, "", false)
	if len(d.SkillsChanged) != 1 {
		t.Fatalf("expected 1 changed skill, got %d", len(d.SkillsChanged))
	}
	sd := d.SkillsChanged[0]
	if sd.Name != "pdf" {
		t.Errorf("expected skill name=pdf, got %s", sd.Name)
	}
	if len(sd.FilesChanged) != 1 || sd.FilesChanged[0] != "prompt.md" {
		t.Errorf("unexpected files changed: %v", sd.FilesChanged)
	}
	if len(sd.FilesAdded) != 1 || sd.FilesAdded[0] != "extra.txt" {
		t.Errorf("unexpected files added: %v", sd.FilesAdded)
	}
}

func TestComputeDiff_SessionCountChanged(t *testing.T) {
	a := newBundle("main")
	a.Files["sessions/main/conv1.json"] = []byte("{}")
	a.Files["sessions/main/conv2.json"] = []byte("{}")

	b := newBundle("main")
	b.Files["sessions/main/conv1.json"] = []byte("{}")
	b.Files["sessions/main/conv2.json"] = []byte("{}")
	b.Files["sessions/main/conv3.json"] = []byte("{}")

	d := computeDiff(a, b, "", false)
	if len(d.SessionsChanged) != 1 {
		t.Fatalf("expected 1 changed session, got %d", len(d.SessionsChanged))
	}
	sd := d.SessionsChanged[0]
	if sd.Slug != "main" || sd.Before != 2 || sd.After != 3 {
		t.Errorf("unexpected session diff: %+v", sd)
	}
}

func TestComputeDiff_PathFilter(t *testing.T) {
	a := newBundle("main", "other")
	setConfig(a, "main", map[string]interface{}{"name": "Main"})
	setConfig(a, "other", map[string]interface{}{"name": "Other"})

	b := newBundle("main", "other")
	setConfig(b, "main", map[string]interface{}{"name": "Main Changed"})
	setConfig(b, "other", map[string]interface{}{"name": "Other Changed"})

	d := computeDiff(a, b, "main", false)
	if len(d.GroupsChanged) != 1 || d.GroupsChanged[0].Slug != "main" {
		t.Errorf("path filter should restrict to 'main' only, got changed=%v", d.GroupsChanged)
	}
}

func TestIsTextContent_Text(t *testing.T) {
	if !isTextContent([]byte("hello world\nline 2\n")) {
		t.Error("expected text content to be detected as text")
	}
}

func TestIsTextContent_Binary(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01, 0x02} // PNG-like with null byte
	if isTextContent(data) {
		t.Error("expected binary content to be detected as binary")
	}
}

func TestUnifiedDiff_Basic(t *testing.T) {
	a := []byte("line one\nline two\nline three\n")
	b := []byte("line one\nline TWO\nline three\n")

	out := unifiedDiff("a/test.md", "b/test.md", a, b)
	if out == "" {
		t.Fatal("expected non-empty diff output")
	}
	if !strings.Contains(out, "--- a/test.md") {
		t.Error("expected '--- a/test.md' in output")
	}
	if !strings.Contains(out, "+++ b/test.md") {
		t.Error("expected '+++ b/test.md' in output")
	}
	if !strings.Contains(out, "-line two") {
		t.Error("expected deletion line in output")
	}
	if !strings.Contains(out, "+line TWO") {
		t.Error("expected insertion line in output")
	}
}

func TestUnifiedDiff_Identical(t *testing.T) {
	a := []byte("same\n")
	out := unifiedDiff("a/x", "b/x", a, a)
	if out != "" {
		t.Errorf("expected empty diff for identical content, got: %q", out)
	}
}
