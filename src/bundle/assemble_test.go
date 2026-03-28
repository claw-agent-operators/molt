// SPDX-License-Identifier: AGPL-3.0-or-later
package bundle

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// feedMessages drives an Assembler to completion with the given messages,
// appending an export_complete at the end.
func feedMessages(t *testing.T, a *Assembler, msgs []map[string]interface{}) {
	t.Helper()
	for _, m := range msgs {
		done, err := a.Feed(m)
		if err != nil {
			t.Fatalf("Feed error: %v", err)
		}
		if done {
			t.Fatal("Feed returned done before export_complete")
		}
	}
	done, err := a.Feed(map[string]interface{}{"type": "export_complete"})
	if err != nil {
		t.Fatalf("Feed(export_complete) error: %v", err)
	}
	if !done {
		t.Fatal("Feed(export_complete) did not return done")
	}
}

func groupMsg(slug string) map[string]interface{} {
	cfg, _ := json.Marshal(map[string]string{"name": slug})
	return map[string]interface{}{
		"type":   "group",
		"slug":   slug,
		"config": json.RawMessage(cfg),
		"files":  []interface{}{},
	}
}

func sessionMsg(slug string) map[string]interface{} {
	content := base64.StdEncoding.EncodeToString([]byte("data"))
	return map[string]interface{}{
		"type": "session",
		"slug": slug,
		"files": []interface{}{
			map[string]interface{}{"path": "session.jsonl", "content": content},
		},
	}
}

func skillManifestMsg(skills map[string][]string) map[string]interface{} {
	raw := map[string]interface{}{}
	for name, slugs := range skills {
		iface := make([]interface{}, len(slugs))
		for i, s := range slugs {
			iface[i] = s
		}
		raw[name] = iface
	}
	return map[string]interface{}{"type": "skill_manifest", "skills": raw}
}

// ── exclude: group filtering ──────────────────────────────────────────────────

func TestExclude_DropsGroup(t *testing.T) {
	a := NewAssembler("test", "1.0", []string{"beta"}, nil)
	feedMessages(t, a, []map[string]interface{}{
		groupMsg("alpha"),
		groupMsg("beta"),
	})

	b := a.Bundle()
	if _, ok := b.Files["groups/beta/config.json"]; ok {
		t.Error("excluded group beta should not be in bundle files")
	}
	for _, g := range b.Manifest.Groups {
		if g == "beta" {
			t.Error("excluded group beta should not be in manifest")
		}
	}
	if _, ok := b.Files["groups/alpha/config.json"]; !ok {
		t.Error("non-excluded group alpha should be in bundle")
	}
}

func TestExclude_ReportsExcluded(t *testing.T) {
	a := NewAssembler("test", "1.0", []string{"beta"}, nil)
	feedMessages(t, a, []map[string]interface{}{groupMsg("alpha"), groupMsg("beta")})

	got := a.Excluded()
	if len(got) != 1 || got[0] != "beta" {
		t.Errorf("Excluded() = %v, want [beta]", got)
	}
}

func TestExclude_NoExcludes(t *testing.T) {
	a := NewAssembler("test", "1.0", nil, nil)
	feedMessages(t, a, []map[string]interface{}{groupMsg("alpha"), groupMsg("beta")})

	if len(a.Excluded()) != 0 {
		t.Errorf("expected no excluded slugs, got %v", a.Excluded())
	}
	b := a.Bundle()
	if len(b.Manifest.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(b.Manifest.Groups))
	}
}

// ── exclude: session filtering ────────────────────────────────────────────────

func TestExclude_DropsSession(t *testing.T) {
	a := NewAssembler("test", "1.0", []string{"beta"}, nil)
	feedMessages(t, a, []map[string]interface{}{
		groupMsg("alpha"),
		groupMsg("beta"),
		sessionMsg("alpha"),
		sessionMsg("beta"),
	})

	b := a.Bundle()
	for path := range b.Files {
		if strings.HasPrefix(path, "sessions/beta/") {
			t.Errorf("excluded session path %q should not be in bundle", path)
		}
	}
	if _, ok := b.Files["sessions/alpha/session.jsonl"]; !ok {
		t.Error("non-excluded session alpha should be in bundle")
	}
}

// ── exclude: skill manifest filtering ────────────────────────────────────────

func TestExclude_FiltersSkillManifest(t *testing.T) {
	a := NewAssembler("test", "1.0", []string{"beta"}, nil)
	feedMessages(t, a, []map[string]interface{}{
		groupMsg("alpha"),
		groupMsg("beta"),
		// "tool" is owned by both alpha and beta; "other" is owned only by beta
		skillManifestMsg(map[string][]string{
			"tool":  {"alpha", "beta"},
			"other": {"beta"},
		}),
	})

	b := a.Bundle()
	if slugs, ok := b.Manifest.Skills["tool"]; !ok {
		t.Error("skill tool should still be in manifest (alpha not excluded)")
	} else {
		for _, s := range slugs {
			if s == "beta" {
				t.Error("excluded slug beta should be removed from skill tool's group list")
			}
		}
		if len(slugs) != 1 || slugs[0] != "alpha" {
			t.Errorf("skill tool slugs = %v, want [alpha]", slugs)
		}
	}
	if _, ok := b.Manifest.Skills["other"]; ok {
		t.Error("skill other should be absent — all its groups were excluded")
	}
}

// ── exclude: unmatched slug warning ──────────────────────────────────────────

func TestExclude_WarnOnUnmatched(t *testing.T) {
	a := NewAssembler("test", "1.0", []string{"bogus"}, nil)
	feedMessages(t, a, []map[string]interface{}{groupMsg("alpha")})

	warnings := a.Bundle().Manifest.Warnings
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "bogus") && strings.Contains(w, "not found") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unmatched-exclude warning for bogus, got warnings: %v", warnings)
	}
}

func TestExclude_NoWarnOnMatched(t *testing.T) {
	a := NewAssembler("test", "1.0", []string{"beta"}, nil)
	feedMessages(t, a, []map[string]interface{}{groupMsg("alpha"), groupMsg("beta")})

	for _, w := range a.Bundle().Manifest.Warnings {
		if strings.Contains(w, "not found") {
			t.Errorf("unexpected unmatched-exclude warning: %q", w)
		}
	}
}

// ── include: group filtering ─────────────────────────────────────────────────

func TestInclude_KeepsOnlyIncluded(t *testing.T) {
	a := NewAssembler("test", "1.0", nil, []string{"alpha"})
	feedMessages(t, a, []map[string]interface{}{
		groupMsg("alpha"),
		groupMsg("beta"),
		groupMsg("gamma"),
	})

	b := a.Bundle()
	if len(b.Manifest.Groups) != 1 || b.Manifest.Groups[0] != "alpha" {
		t.Errorf("expected only [alpha] in manifest groups, got %v", b.Manifest.Groups)
	}
	if _, ok := b.Files["groups/alpha/config.json"]; !ok {
		t.Error("included group alpha should be in bundle")
	}
	if _, ok := b.Files["groups/beta/config.json"]; ok {
		t.Error("non-included group beta should not be in bundle")
	}
	if _, ok := b.Files["groups/gamma/config.json"]; ok {
		t.Error("non-included group gamma should not be in bundle")
	}
}

func TestInclude_ReportsExcluded(t *testing.T) {
	a := NewAssembler("test", "1.0", nil, []string{"alpha"})
	feedMessages(t, a, []map[string]interface{}{
		groupMsg("alpha"),
		groupMsg("beta"),
		groupMsg("gamma"),
	})

	got := a.Excluded()
	if len(got) != 2 {
		t.Errorf("expected 2 excluded slugs, got %v", got)
	}
	excluded := map[string]bool{}
	for _, s := range got {
		excluded[s] = true
	}
	if !excluded["beta"] || !excluded["gamma"] {
		t.Errorf("expected beta and gamma excluded, got %v", got)
	}
}

// ── include: session filtering ───────────────────────────────────────────────

func TestInclude_DropsNonIncludedSessions(t *testing.T) {
	a := NewAssembler("test", "1.0", nil, []string{"alpha"})
	feedMessages(t, a, []map[string]interface{}{
		groupMsg("alpha"),
		groupMsg("beta"),
		sessionMsg("alpha"),
		sessionMsg("beta"),
	})

	b := a.Bundle()
	if _, ok := b.Files["sessions/alpha/session.jsonl"]; !ok {
		t.Error("included session alpha should be in bundle")
	}
	for path := range b.Files {
		if strings.HasPrefix(path, "sessions/beta/") {
			t.Errorf("non-included session path %q should not be in bundle", path)
		}
	}
}

// ── include: skill manifest filtering ────────────────────────────────────────

func TestInclude_FiltersSkillManifest(t *testing.T) {
	a := NewAssembler("test", "1.0", nil, []string{"alpha"})
	feedMessages(t, a, []map[string]interface{}{
		groupMsg("alpha"),
		groupMsg("beta"),
		skillManifestMsg(map[string][]string{
			"tool":  {"alpha", "beta"},
			"other": {"beta"},
		}),
	})

	b := a.Bundle()
	if slugs, ok := b.Manifest.Skills["tool"]; !ok {
		t.Error("skill tool should still be in manifest (alpha is included)")
	} else {
		if len(slugs) != 1 || slugs[0] != "alpha" {
			t.Errorf("skill tool slugs = %v, want [alpha]", slugs)
		}
	}
	if _, ok := b.Manifest.Skills["other"]; ok {
		t.Error("skill other should be absent — its only group was not included")
	}
}

// ── include: unmatched slug warning ──────────────────────────────────────────

func TestInclude_WarnOnUnmatched(t *testing.T) {
	a := NewAssembler("test", "1.0", nil, []string{"alpha", "missing"})
	feedMessages(t, a, []map[string]interface{}{groupMsg("alpha")})

	warnings := a.Bundle().Manifest.Warnings
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "missing") && strings.Contains(w, "not found") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unmatched-include warning for missing, got warnings: %v", warnings)
	}
}

func TestInclude_NoWarnOnMatched(t *testing.T) {
	a := NewAssembler("test", "1.0", nil, []string{"alpha"})
	feedMessages(t, a, []map[string]interface{}{groupMsg("alpha")})

	for _, w := range a.Bundle().Manifest.Warnings {
		if strings.Contains(w, "not found") {
			t.Errorf("unexpected unmatched warning: %q", w)
		}
	}
}
