// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kenbolton/molt/src/bundle"
	"github.com/spf13/cobra"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// BundleDiff holds the computed difference between two bundles.
type BundleDiff struct {
	GroupsAdded     []string
	GroupsRemoved   []string
	GroupsChanged   []GroupDiff
	TasksAdded      []TaskRecord
	TasksRemoved    []TaskRecord
	TasksChanged    []TaskChangeDiff
	SkillsAdded     []string
	SkillsRemoved   []string
	SkillsChanged   []SkillDiff
	SessionsAdded   []SessionCountRecord
	SessionsRemoved []SessionCountRecord
	SessionsChanged []SessionCountDiff
	SecretsChanged  bool
	SecretsAdded    []string
	SecretsRemoved  []string
}

// HasDifferences returns true if any difference was detected.
func (d *BundleDiff) HasDifferences() bool {
	return len(d.GroupsAdded) > 0 || len(d.GroupsRemoved) > 0 || len(d.GroupsChanged) > 0 ||
		len(d.TasksAdded) > 0 || len(d.TasksRemoved) > 0 || len(d.TasksChanged) > 0 ||
		len(d.SkillsAdded) > 0 || len(d.SkillsRemoved) > 0 || len(d.SkillsChanged) > 0 ||
		len(d.SessionsAdded) > 0 || len(d.SessionsRemoved) > 0 || len(d.SessionsChanged) > 0 ||
		d.SecretsChanged
}

// GroupDiff holds the diff for a single group.
type GroupDiff struct {
	Slug          string
	ConfigChanges []FieldChange
	FilesAdded    []string
	FilesRemoved  []string
	FilesChanged  []FileChange
}

// FieldChange records a change to a single named field.
type FieldChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// FileChange records a changed file, with optional unified diff content.
type FileChange struct {
	Path        string
	IsBinary    bool
	UnifiedDiff string
}

// TaskRecord is a snapshot of a scheduled task.
type TaskRecord struct {
	ID            string `json:"id"`
	GroupSlug     string `json:"group_slug"`
	ScheduleType  string `json:"schedule_type"`
	ScheduleValue string `json:"schedule_value"`
	Prompt        string `json:"prompt"`
	Active        bool   `json:"active"`
}

// TaskChangeDiff records field-level changes to a task.
type TaskChangeDiff struct {
	ID      string        `json:"id"`
	Changes []FieldChange `json:"changes"`
}

// SkillDiff records file-level changes within a skill.
type SkillDiff struct {
	Name         string   `json:"name"`
	FilesAdded   []string `json:"files_added"`
	FilesRemoved []string `json:"files_removed"`
	FilesChanged []string `json:"files_changed"`
}

// SessionCountRecord is a session file-count snapshot for one slug.
type SessionCountRecord struct {
	Slug      string `json:"slug"`
	FileCount int    `json:"file_count"`
}

// SessionCountDiff records a count change for a session slug.
type SessionCountDiff struct {
	Slug   string `json:"slug"`
	Before int    `json:"before"`
	After  int    `json:"after"`
}

// ── Command ───────────────────────────────────────────────────────────────────

var diffCmd = &cobra.Command{
	Use:               "diff <bundle1> <bundle2>",
	Short:             "Show what changed between two .molt bundles",
	Args:              cobra.ExactArgs(2),
	RunE:              runDiff,
	ValidArgsFunction: completeTwoBundles,
}

var (
	diffStat   bool
	diffPath   string
	diffFormat string
	diffPatch  bool
)

func init() {
	diffCmd.Flags().BoolVar(&diffStat, "stat", false, "Summary counts only, no per-item detail")
	diffCmd.Flags().StringVar(&diffPath, "path", "", "Scope diff to one group slug")
	diffCmd.Flags().StringVar(&diffFormat, "format", "text", "Output format: text (default) | json")
	diffCmd.Flags().BoolVar(&diffPatch, "patch", false, "Inline unified diff for text files (text format only)")
	rootCmd.AddCommand(diffCmd)
}

// completeTwoBundles completes up to two .molt file arguments.
func completeTwoBundles(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) <= 1 {
		return []string{"molt"}, cobra.ShellCompDirectiveFilterFileExt
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func runDiff(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if diffStat && diffPatch {
		return fmt.Errorf("--stat and --patch are incompatible")
	}
	if diffStat && diffFormat == "json" {
		return fmt.Errorf("--stat is not available with --format=json")
	}

	a, err := bundle.Load(args[0])
	if err != nil {
		return fmt.Errorf("cannot load %s: %w", args[0], err)
	}
	b, err := bundle.Load(args[1])
	if err != nil {
		return fmt.Errorf("cannot load %s: %w", args[1], err)
	}

	if a.Manifest.MoltVersion != b.Manifest.MoltVersion {
		fmt.Fprintf(os.Stderr, "warning: bundle format versions differ (%s vs %s) — diff may be incomplete\n",
			a.Manifest.MoltVersion, b.Manifest.MoltVersion)
	}

	if diffPath != "" {
		inA, inB := false, false
		for _, s := range a.Manifest.Groups {
			if s == diffPath {
				inA = true
				break
			}
		}
		for _, s := range b.Manifest.Groups {
			if s == diffPath {
				inB = true
				break
			}
		}
		if !inA && !inB {
			fmt.Fprintf(os.Stderr, "warning: slug %q not found in either bundle\n", diffPath)
			os.Exit(2)
		}
	}

	d := computeDiff(a, b, diffPath, diffPatch)

	switch diffFormat {
	case "json":
		printDiffJSON(d, args[0], args[1])
	case "text":
		if diffStat {
			printDiffStat(d, args[0], args[1])
		} else {
			printDiffText(d, args[0], args[1], a, b, diffPatch)
		}
	default:
		return fmt.Errorf("unknown format %q: use text or json", diffFormat)
	}

	if d.HasDifferences() {
		os.Exit(1)
	}
	return nil
}

// ── Core diff ─────────────────────────────────────────────────────────────────

func computeDiff(a, b *bundle.Bundle, pathFilter string, withPatch bool) *BundleDiff {
	d := &BundleDiff{}

	// ── Groups ────────────────────────────────────────────────────────────────
	aGroupSet := make(map[string]bool, len(a.Manifest.Groups))
	for _, s := range a.Manifest.Groups {
		aGroupSet[s] = true
	}
	bGroupSet := make(map[string]bool, len(b.Manifest.Groups))
	for _, s := range b.Manifest.Groups {
		bGroupSet[s] = true
	}

	for _, s := range a.Manifest.Groups {
		if pathFilter != "" && s != pathFilter {
			continue
		}
		if !bGroupSet[s] {
			d.GroupsRemoved = append(d.GroupsRemoved, s)
		}
	}
	for _, s := range b.Manifest.Groups {
		if pathFilter != "" && s != pathFilter {
			continue
		}
		if !aGroupSet[s] {
			d.GroupsAdded = append(d.GroupsAdded, s)
		}
	}
	sort.Strings(d.GroupsAdded)
	sort.Strings(d.GroupsRemoved)

	for _, s := range a.Manifest.Groups {
		if pathFilter != "" && s != pathFilter {
			continue
		}
		if !bGroupSet[s] {
			continue
		}
		prefix := "groups/" + s + "/"
		aGroupFiles := extractPrefix(a.Files, prefix)
		bGroupFiles := extractPrefix(b.Files, prefix)

		var configChanges []FieldChange
		aConfig, aHasCfg := aGroupFiles["config.json"]
		bConfig, bHasCfg := bGroupFiles["config.json"]
		if aHasCfg && bHasCfg && !bytes.Equal(aConfig, bConfig) {
			configChanges, _ = diffConfigJSON(aConfig, bConfig)
		}

		aMemFiles := withoutKey(aGroupFiles, "config.json")
		bMemFiles := withoutKey(bGroupFiles, "config.json")
		added, removed, changed := compareFiles(aMemFiles, bMemFiles, withPatch)

		if len(configChanges) > 0 || len(added) > 0 || len(removed) > 0 || len(changed) > 0 {
			d.GroupsChanged = append(d.GroupsChanged, GroupDiff{
				Slug:          s,
				ConfigChanges: configChanges,
				FilesAdded:    added,
				FilesRemoved:  removed,
				FilesChanged:  changed,
			})
		}
	}
	sort.Slice(d.GroupsChanged, func(i, j int) bool {
		return d.GroupsChanged[i].Slug < d.GroupsChanged[j].Slug
	})

	// ── Tasks ─────────────────────────────────────────────────────────────────
	aTasks := parseTasks(a.Files)
	bTasks := parseTasks(b.Files)

	aTaskMap := make(map[string]TaskRecord, len(aTasks))
	for _, t := range aTasks {
		aTaskMap[t.ID] = t
	}
	bTaskMap := make(map[string]TaskRecord, len(bTasks))
	for _, t := range bTasks {
		bTaskMap[t.ID] = t
	}

	for id, t := range aTaskMap {
		if _, ok := bTaskMap[id]; !ok {
			if pathFilter == "" || t.GroupSlug == pathFilter {
				d.TasksRemoved = append(d.TasksRemoved, t)
			}
		}
	}
	for id, t := range bTaskMap {
		if _, ok := aTaskMap[id]; !ok {
			if pathFilter == "" || t.GroupSlug == pathFilter {
				d.TasksAdded = append(d.TasksAdded, t)
			}
		}
	}
	for id, at := range aTaskMap {
		bt, ok := bTaskMap[id]
		if !ok {
			continue
		}
		if pathFilter != "" && at.GroupSlug != pathFilter && bt.GroupSlug != pathFilter {
			continue
		}
		var changes []FieldChange
		if at.GroupSlug != bt.GroupSlug {
			changes = append(changes, FieldChange{"group_slug", at.GroupSlug, bt.GroupSlug})
		}
		if at.ScheduleType != bt.ScheduleType {
			changes = append(changes, FieldChange{"schedule_type", at.ScheduleType, bt.ScheduleType})
		}
		if at.ScheduleValue != bt.ScheduleValue {
			changes = append(changes, FieldChange{"schedule_value", at.ScheduleValue, bt.ScheduleValue})
		}
		if at.Prompt != bt.Prompt {
			changes = append(changes, FieldChange{"prompt", at.Prompt, bt.Prompt})
		}
		if at.Active != bt.Active {
			from, to := "true", "false"
			if bt.Active {
				from, to = "false", "true"
			}
			changes = append(changes, FieldChange{"active", from, to})
		}
		if len(changes) > 0 {
			d.TasksChanged = append(d.TasksChanged, TaskChangeDiff{id, changes})
		}
	}
	sort.Slice(d.TasksAdded, func(i, j int) bool { return d.TasksAdded[i].ID < d.TasksAdded[j].ID })
	sort.Slice(d.TasksRemoved, func(i, j int) bool { return d.TasksRemoved[i].ID < d.TasksRemoved[j].ID })
	sort.Slice(d.TasksChanged, func(i, j int) bool { return d.TasksChanged[i].ID < d.TasksChanged[j].ID })

	// ── Skills ────────────────────────────────────────────────────────────────
	if pathFilter == "" {
		aSkills := enumerateSkills(a.Files)
		bSkills := enumerateSkills(b.Files)

		aSkillSet := make(map[string]bool, len(aSkills))
		for _, s := range aSkills {
			aSkillSet[s] = true
		}
		bSkillSet := make(map[string]bool, len(bSkills))
		for _, s := range bSkills {
			bSkillSet[s] = true
		}

		for _, s := range aSkills {
			if !bSkillSet[s] {
				d.SkillsRemoved = append(d.SkillsRemoved, s)
			}
		}
		for _, s := range bSkills {
			if !aSkillSet[s] {
				d.SkillsAdded = append(d.SkillsAdded, s)
			}
		}
		sort.Strings(d.SkillsAdded)
		sort.Strings(d.SkillsRemoved)

		for _, name := range aSkills {
			if !bSkillSet[name] {
				continue
			}
			prefix := "skills/" + name + "/"
			aSkillFiles := extractPrefix(a.Files, prefix)
			bSkillFiles := extractPrefix(b.Files, prefix)
			// Skills show file names only — no inline patch regardless of withPatch.
			added, removed, changed := compareFiles(aSkillFiles, bSkillFiles, false)
			if len(added) > 0 || len(removed) > 0 || len(changed) > 0 {
				changedNames := make([]string, len(changed))
				for i, c := range changed {
					changedNames[i] = c.Path
				}
				d.SkillsChanged = append(d.SkillsChanged, SkillDiff{
					Name:         name,
					FilesAdded:   added,
					FilesRemoved: removed,
					FilesChanged: changedNames,
				})
			}
		}
		sort.Slice(d.SkillsChanged, func(i, j int) bool {
			return d.SkillsChanged[i].Name < d.SkillsChanged[j].Name
		})
	}

	// ── Sessions ──────────────────────────────────────────────────────────────
	if pathFilter == "" {
		aSessions := countSessions(a.Files)
		bSessions := countSessions(b.Files)

		for slug, count := range aSessions {
			if _, ok := bSessions[slug]; !ok {
				d.SessionsRemoved = append(d.SessionsRemoved, SessionCountRecord{slug, count})
			}
		}
		for slug, count := range bSessions {
			if _, ok := aSessions[slug]; !ok {
				d.SessionsAdded = append(d.SessionsAdded, SessionCountRecord{slug, count})
			}
		}
		for slug, ac := range aSessions {
			bc, ok := bSessions[slug]
			if !ok {
				continue
			}
			if ac != bc {
				d.SessionsChanged = append(d.SessionsChanged, SessionCountDiff{slug, ac, bc})
			}
		}
		sort.Slice(d.SessionsAdded, func(i, j int) bool { return d.SessionsAdded[i].Slug < d.SessionsAdded[j].Slug })
		sort.Slice(d.SessionsRemoved, func(i, j int) bool { return d.SessionsRemoved[i].Slug < d.SessionsRemoved[j].Slug })
		sort.Slice(d.SessionsChanged, func(i, j int) bool { return d.SessionsChanged[i].Slug < d.SessionsChanged[j].Slug })
	}

	// ── Secrets ───────────────────────────────────────────────────────────────
	if pathFilter == "" {
		aKeys := parseSecretsKeys(a.Files)
		bKeys := parseSecretsKeys(b.Files)

		aKeySet := make(map[string]bool, len(aKeys))
		for _, k := range aKeys {
			aKeySet[k] = true
		}
		bKeySet := make(map[string]bool, len(bKeys))
		for _, k := range bKeys {
			bKeySet[k] = true
		}
		for _, k := range aKeys {
			if !bKeySet[k] {
				d.SecretsRemoved = append(d.SecretsRemoved, k)
			}
		}
		for _, k := range bKeys {
			if !aKeySet[k] {
				d.SecretsAdded = append(d.SecretsAdded, k)
			}
		}
		sort.Strings(d.SecretsAdded)
		sort.Strings(d.SecretsRemoved)
		d.SecretsChanged = len(d.SecretsAdded) > 0 || len(d.SecretsRemoved) > 0
	}

	return d
}

// compareFiles compares two flat maps of filename → content (no path prefix).
// Returns added/removed filenames and changed FileChange entries.
func compareFiles(aFiles, bFiles map[string][]byte, withPatch bool) (added, removed []string, changed []FileChange) {
	for name := range bFiles {
		if _, ok := aFiles[name]; !ok {
			added = append(added, name)
		}
	}
	for name := range aFiles {
		if _, ok := bFiles[name]; !ok {
			removed = append(removed, name)
		}
	}
	for name, aData := range aFiles {
		bData, ok := bFiles[name]
		if !ok {
			continue
		}
		if bytes.Equal(aData, bData) {
			continue
		}
		fc := FileChange{Path: name}
		if !isTextContent(aData) || !isTextContent(bData) {
			fc.IsBinary = true
		} else if withPatch {
			fc.UnifiedDiff = unifiedDiff("a/"+name, "b/"+name, aData, bData)
		}
		changed = append(changed, fc)
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Slice(changed, func(i, j int) bool { return changed[i].Path < changed[j].Path })
	return
}

// diffConfigJSON returns field-level changes between two config.json byte slices.
func diffConfigJSON(aData, bData []byte) ([]FieldChange, error) {
	var aMap, bMap map[string]interface{}
	if err := json.Unmarshal(aData, &aMap); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(bData, &bMap); err != nil {
		return nil, err
	}
	keys := make(map[string]bool)
	for k := range aMap {
		keys[k] = true
	}
	for k := range bMap {
		keys[k] = true
	}
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var changes []FieldChange
	for _, k := range sortedKeys {
		av, aOk := aMap[k]
		bv, bOk := bMap[k]
		aStr := jsonValueStr(av, aOk)
		bStr := jsonValueStr(bv, bOk)
		if aStr != bStr {
			changes = append(changes, FieldChange{k, aStr, bStr})
		}
	}
	return changes, nil
}

func jsonValueStr(v interface{}, present bool) string {
	if !present {
		return "(missing)"
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// isTextContent returns true if data appears to be valid UTF-8 text.
func isTextContent(data []byte) bool {
	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	return bytes.IndexByte(check, 0) < 0 && utf8.Valid(check)
}

// ── Myers diff / unified diff ─────────────────────────────────────────────────

type editKind int

const (
	editEqual  editKind = iota
	editInsert          // line present in b
	editDelete          // line present in a
)

type diffEdit struct {
	kind editKind
	line string
}

// computeEdits computes the minimal edit script from a to b using LCS.
// Space is O(m×n); only call this for text files of reasonable size (--patch).
func computeEdits(a, b []string) []diffEdit {
	m, n := len(a), len(b)
	// DP table for LCS lengths.
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	// Backtrack to produce edit sequence (reversed, then flipped).
	edits := make([]diffEdit, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && a[i-1] == b[j-1] {
			edits = append(edits, diffEdit{editEqual, a[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			edits = append(edits, diffEdit{editInsert, b[j-1]})
			j--
		} else {
			edits = append(edits, diffEdit{editDelete, a[i-1]})
			i--
		}
	}
	// Reverse in-place.
	for l, r := 0, len(edits)-1; l < r; l, r = l+1, r-1 {
		edits[l], edits[r] = edits[r], edits[l]
	}
	return edits
}

// unifiedDiff produces a unified diff string for two text byte slices.
func unifiedDiff(pathA, pathB string, a, b []byte) string {
	aLines := splitLines(string(a))
	bLines := splitLines(string(b))
	edits := computeEdits(aLines, bLines)
	if len(edits) == 0 {
		return ""
	}

	const ctx = 3

	// Find positions of all changes.
	changePos := []int{}
	for i, e := range edits {
		if e.kind != editEqual {
			changePos = append(changePos, i)
		}
	}
	if len(changePos) == 0 {
		return ""
	}

	// Merge nearby changes into hunk ranges.
	type hunkRange struct{ start, end int }
	var hunks []hunkRange
	i := 0
	for i < len(changePos) {
		start := changePos[i]
		if start > ctx {
			start -= ctx
		} else {
			start = 0
		}
		end := changePos[i] + ctx
		if end >= len(edits) {
			end = len(edits) - 1
		}
		j := i + 1
		for j < len(changePos) {
			nextCtxStart := changePos[j] - ctx
			if nextCtxStart < 0 {
				nextCtxStart = 0
			}
			if nextCtxStart <= end+1 {
				end = changePos[j] + ctx
				if end >= len(edits) {
					end = len(edits) - 1
				}
				j++
			} else {
				break
			}
		}
		hunks = append(hunks, hunkRange{start, end})
		i = j
	}

	var sb strings.Builder
	sb.WriteString("--- " + pathA + "\n")
	sb.WriteString("+++ " + pathB + "\n")

	for _, h := range hunks {
		hunkEdits := edits[h.start : h.end+1]
		aStart, bStart, aCount, bCount := 0, 0, 0, 0
		for k := 0; k < h.start; k++ {
			if edits[k].kind != editInsert {
				aStart++
			}
			if edits[k].kind != editDelete {
				bStart++
			}
		}
		for _, e := range hunkEdits {
			if e.kind != editInsert {
				aCount++
			}
			if e.kind != editDelete {
				bCount++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", aStart+1, aCount, bStart+1, bCount)
		for _, e := range hunkEdits {
			switch e.kind {
			case editEqual:
				sb.WriteString(" " + e.line + "\n")
			case editInsert:
				sb.WriteString("+" + e.line + "\n")
			case editDelete:
				sb.WriteString("-" + e.line + "\n")
			}
		}
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// extractPrefix returns a new map with the given prefix stripped from all keys.
func extractPrefix(files map[string][]byte, prefix string) map[string][]byte {
	out := make(map[string][]byte)
	for k, v := range files {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return out
}

// withoutKey returns a shallow copy of m without the given key.
func withoutKey(m map[string][]byte, key string) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}

// parseTasks parses tasks.json from the bundle files map.
func parseTasks(files map[string][]byte) []TaskRecord {
	data, ok := files["tasks.json"]
	if !ok {
		return nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	tasks := make([]TaskRecord, 0, len(raw))
	for _, m := range raw {
		t := TaskRecord{}
		t.ID, _ = m["id"].(string)
		t.GroupSlug, _ = m["group_slug"].(string)
		t.ScheduleType, _ = m["schedule_type"].(string)
		t.ScheduleValue, _ = m["schedule_value"].(string)
		t.Prompt, _ = m["prompt"].(string)
		t.Active, _ = m["active"].(bool)
		tasks = append(tasks, t)
	}
	return tasks
}

// enumerateSkills returns all unique skill names found in the Files map.
func enumerateSkills(files map[string][]byte) []string {
	seen := make(map[string]bool)
	for path := range files {
		if !strings.HasPrefix(path, "skills/") {
			continue
		}
		rest := strings.TrimPrefix(path, "skills/")
		if idx := strings.Index(rest, "/"); idx > 0 {
			seen[rest[:idx]] = true
		}
	}
	names := make([]string, 0, len(seen))
	for s := range seen {
		names = append(names, s)
	}
	sort.Strings(names)
	return names
}

// countSessions returns file counts per session slug.
func countSessions(files map[string][]byte) map[string]int {
	counts := make(map[string]int)
	for path := range files {
		if !strings.HasPrefix(path, "sessions/") {
			continue
		}
		rest := strings.TrimPrefix(path, "sessions/")
		if idx := strings.Index(rest, "/"); idx > 0 {
			counts[rest[:idx]]++
		}
	}
	return counts
}

// parseSecretsKeys extracts key names from secrets-template.env.
func parseSecretsKeys(files map[string][]byte) []string {
	data, ok := files["secrets-template.env"]
	if !ok {
		return nil
	}
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			keys = append(keys, line[:idx])
		}
	}
	return keys
}

// ── Output: text ──────────────────────────────────────────────────────────────

func bundleHeader(path string, m *bundle.Manifest) string {
	arch := m.Source.Arch
	ver := m.Source.ArchVersion
	if ver != "" && ver != "unknown" {
		return fmt.Sprintf("%s  (%s v%s, %s)", filepath.Base(path), arch, ver, m.CreatedAt)
	}
	return fmt.Sprintf("%s  (%s, %s)", filepath.Base(path), arch, m.CreatedAt)
}

func printDiffText(d *BundleDiff, pathA, pathB string, a, b *bundle.Bundle, patch bool) {
	fmt.Printf("--- %s\n", bundleHeader(pathA, a.Manifest))
	fmt.Printf("+++ %s\n", bundleHeader(pathB, b.Manifest))

	hasAny := d.HasDifferences()
	if !hasAny {
		fmt.Println("\nBundles are identical.")
		return
	}
	fmt.Println()

	// ── Groups ────────────────────────────────────────────────────────────────
	if len(d.GroupsAdded)+len(d.GroupsRemoved)+len(d.GroupsChanged) > 0 {
		fmt.Println("Groups:")
		for _, slug := range d.GroupsAdded {
			prefix := "groups/" + slug + "/"
			count := 0
			for k := range b.Files {
				if strings.HasPrefix(k, prefix) {
					count++
				}
			}
			fmt.Printf("  + %-30s  (added, %d file(s))\n", slug, count)
		}
		for _, slug := range d.GroupsRemoved {
			fmt.Printf("  - %-30s  (removed)\n", slug)
		}
		for _, gd := range d.GroupsChanged {
			desc := groupChangeDesc(gd)
			fmt.Printf("  ~ %-30s  %s\n", gd.Slug, desc)
			if patch {
				for _, fc := range gd.FilesChanged {
					if fc.UnifiedDiff != "" {
						fmt.Print(fc.UnifiedDiff)
					}
				}
			}
		}
		fmt.Println()
	}

	// ── Tasks ─────────────────────────────────────────────────────────────────
	if len(d.TasksAdded)+len(d.TasksRemoved)+len(d.TasksChanged) > 0 {
		fmt.Println("Tasks:")
		for _, t := range d.TasksAdded {
			fmt.Printf("  + %-30s  %s %q → %s\n", t.ID, t.ScheduleType, t.ScheduleValue, t.GroupSlug)
		}
		for _, t := range d.TasksRemoved {
			fmt.Printf("  - %-30s  (removed)\n", t.ID)
		}
		for _, td := range d.TasksChanged {
			desc := taskChangeDesc(td.Changes)
			fmt.Printf("  ~ %-30s  %s\n", td.ID, desc)
		}
		fmt.Println()
	}

	// ── Skills ────────────────────────────────────────────────────────────────
	if len(d.SkillsAdded)+len(d.SkillsRemoved)+len(d.SkillsChanged) > 0 {
		fmt.Println("Skills:")
		for _, name := range d.SkillsAdded {
			fmt.Printf("  + %-30s  (added)\n", name+"/")
		}
		for _, name := range d.SkillsRemoved {
			fmt.Printf("  - %-30s  (removed)\n", name+"/")
		}
		for _, sd := range d.SkillsChanged {
			total := len(sd.FilesAdded) + len(sd.FilesRemoved) + len(sd.FilesChanged)
			fmt.Printf("  ~ %-30s  %d file(s) changed\n", sd.Name+"/", total)
		}
		fmt.Println()
	}

	// ── Sessions ──────────────────────────────────────────────────────────────
	if len(d.SessionsAdded)+len(d.SessionsRemoved)+len(d.SessionsChanged) > 0 {
		fmt.Println("Sessions:")
		for _, sr := range d.SessionsAdded {
			fmt.Printf("  + %-30s  (new, %d file(s))\n", sr.Slug, sr.FileCount)
		}
		for _, sr := range d.SessionsRemoved {
			fmt.Printf("  - %-30s  (removed, %d file(s))\n", sr.Slug, sr.FileCount)
		}
		for _, sd := range d.SessionsChanged {
			fmt.Printf("  ~ %-30s  %d → %d file(s)\n", sd.Slug, sd.Before, sd.After)
		}
		fmt.Println()
	}

	// ── Secrets ───────────────────────────────────────────────────────────────
	if d.SecretsChanged {
		fmt.Println("Secrets template:")
		for _, k := range d.SecretsAdded {
			fmt.Printf("  + %s\n", k)
		}
		for _, k := range d.SecretsRemoved {
			fmt.Printf("  - %s\n", k)
		}
		fmt.Println()
	}
}

func groupChangeDesc(gd GroupDiff) string {
	var parts []string
	if len(gd.ConfigChanges) == 1 {
		c := gd.ConfigChanges[0]
		parts = append(parts, fmt.Sprintf("config changed: %s %q → %q", c.Field, c.From, c.To))
	} else if len(gd.ConfigChanges) > 1 {
		parts = append(parts, fmt.Sprintf("config changed (%d fields)", len(gd.ConfigChanges)))
	}
	fileCount := len(gd.FilesAdded) + len(gd.FilesRemoved) + len(gd.FilesChanged)
	if fileCount > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s) changed", fileCount))
	}
	return strings.Join(parts, ", ")
}

func taskChangeDesc(changes []FieldChange) string {
	if len(changes) == 0 {
		return "(changed)"
	}
	c := changes[0]
	desc := fmt.Sprintf("%s: %q → %q", c.Field, c.From, c.To)
	if len(changes) > 1 {
		desc += fmt.Sprintf(" (+%d more)", len(changes)-1)
	}
	return desc
}

// ── Output: stat ──────────────────────────────────────────────────────────────

func printDiffStat(d *BundleDiff, pathA, pathB string) {
	fmt.Printf("--- %s  +++ %s\n", filepath.Base(pathA), filepath.Base(pathB))
	if !d.HasDifferences() {
		fmt.Println("  (identical)")
		return
	}
	if len(d.GroupsAdded)+len(d.GroupsRemoved)+len(d.GroupsChanged) > 0 {
		fmt.Printf("  %d group(s) added, %d removed, %d changed\n",
			len(d.GroupsAdded), len(d.GroupsRemoved), len(d.GroupsChanged))
	}
	if len(d.TasksAdded)+len(d.TasksRemoved)+len(d.TasksChanged) > 0 {
		fmt.Printf("  %d task(s) added, %d removed, %d changed\n",
			len(d.TasksAdded), len(d.TasksRemoved), len(d.TasksChanged))
	}
	if len(d.SkillsAdded)+len(d.SkillsRemoved)+len(d.SkillsChanged) > 0 {
		fmt.Printf("  %d skill(s) added, %d removed, %d changed\n",
			len(d.SkillsAdded), len(d.SkillsRemoved), len(d.SkillsChanged))
	}
	sessTotal := len(d.SessionsAdded) + len(d.SessionsRemoved) + len(d.SessionsChanged)
	if sessTotal > 0 {
		fmt.Printf("  %d session group(s) changed\n", sessTotal)
	}
	if d.SecretsChanged {
		fmt.Printf("  %d secret(s) added, %d removed\n", len(d.SecretsAdded), len(d.SecretsRemoved))
	}
}

// ── Output: JSON ──────────────────────────────────────────────────────────────

type diffJSONOutput struct {
	BundleA   string           `json:"bundle_a"`
	BundleB   string           `json:"bundle_b"`
	Groups    diffGroupsJSON   `json:"groups"`
	Tasks     diffTasksJSON    `json:"tasks"`
	Skills    diffSkillsJSON   `json:"skills"`
	Sessions  diffSessionsJSON `json:"sessions"`
	Identical bool             `json:"identical"`
}

type diffGroupsJSON struct {
	Added   []string        `json:"added"`
	Removed []string        `json:"removed"`
	Changed []groupDiffJSON `json:"changed"`
}

type groupDiffJSON struct {
	Slug          string        `json:"slug"`
	ConfigChanges []FieldChange `json:"config_changes"`
	FilesAdded    []string      `json:"files_added"`
	FilesRemoved  []string      `json:"files_removed"`
	FilesChanged  []string      `json:"files_changed"`
}

type diffTasksJSON struct {
	Added   []TaskRecord     `json:"added"`
	Removed []TaskRecord     `json:"removed"`
	Changed []TaskChangeDiff `json:"changed"`
}

type diffSkillsJSON struct {
	Added   []string    `json:"added"`
	Removed []string    `json:"removed"`
	Changed []SkillDiff `json:"changed"`
}

type diffSessionsJSON struct {
	Added        []SessionCountRecord `json:"added"`
	Removed      []SessionCountRecord `json:"removed"`
	CountChanged []SessionCountDiff   `json:"count_changed"`
}

func printDiffJSON(d *BundleDiff, pathA, pathB string) {
	changedGroups := make([]groupDiffJSON, len(d.GroupsChanged))
	for i, gd := range d.GroupsChanged {
		changedPaths := make([]string, len(gd.FilesChanged))
		for j, fc := range gd.FilesChanged {
			changedPaths[j] = fc.Path
		}
		changedGroups[i] = groupDiffJSON{
			Slug:          gd.Slug,
			ConfigChanges: orEmpty(gd.ConfigChanges),
			FilesAdded:    orEmpty(gd.FilesAdded),
			FilesRemoved:  orEmpty(gd.FilesRemoved),
			FilesChanged:  changedPaths,
		}
	}

	out := diffJSONOutput{
		BundleA: filepath.Base(pathA),
		BundleB: filepath.Base(pathB),
		Groups: diffGroupsJSON{
			Added:   orEmpty(d.GroupsAdded),
			Removed: orEmpty(d.GroupsRemoved),
			Changed: changedGroups,
		},
		Tasks: diffTasksJSON{
			Added:   orEmpty(d.TasksAdded),
			Removed: orEmpty(d.TasksRemoved),
			Changed: orEmpty(d.TasksChanged),
		},
		Skills: diffSkillsJSON{
			Added:   orEmpty(d.SkillsAdded),
			Removed: orEmpty(d.SkillsRemoved),
			Changed: orEmpty(d.SkillsChanged),
		},
		Sessions: diffSessionsJSON{
			Added:        orEmpty(d.SessionsAdded),
			Removed:      orEmpty(d.SessionsRemoved),
			CountChanged: orEmpty(d.SessionsChanged),
		},
		Identical: !d.HasDifferences(),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
