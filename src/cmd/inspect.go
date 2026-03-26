package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kenbolton/molt/src/bundle"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <bundle>",
	Short: "Show the contents of a .molt bundle without importing",
	Long: `Inspect a .molt bundle and display its contents: groups, tasks, sessions, and warnings.

Useful for previewing a bundle before importing, or verifying an export.

Examples:
  molt inspect my-agents.molt
  molt inspect backup.molt`,
	Args: cobra.ExactArgs(1),
	RunE: runInspect,
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}

// groupConfigSlim is the subset of GroupConfig we care about for display.
type groupConfigSlim struct {
	Name            string          `json:"name"`
	JID             string          `json:"jid"`
	Trigger         string          `json:"trigger"`
	RequiresTrigger bool            `json:"requires_trigger"`
	IsMain          bool            `json:"is_main"`
	ArchNanoclaw    json.RawMessage `json:"_arch_nanoclaw,omitempty"`
}

type archNanoclawSlim struct {
	SymlinkTarget string `json:"symlink_target"`
}

func runInspect(cmd *cobra.Command, args []string) error {
	bundlePath := args[0]

	b, err := bundle.Load(bundlePath)
	if err != nil {
		return err
	}

	m := b.Manifest

	// ── Header ──────────────────────────────────────────────────────────────
	fmt.Printf("Bundle:   %s\n", filepath.Base(bundlePath))
	fmt.Printf("Created:  %s\n", m.CreatedAt)
	fmt.Printf("Source:   %s", m.Source.Arch)
	if m.Source.ArchVersion != "" && m.Source.ArchVersion != "unknown" {
		fmt.Printf(" v%s", m.Source.ArchVersion)
	}
	if m.Source.Hostname != "" {
		fmt.Printf("  (%s)", m.Source.Hostname)
	}
	fmt.Println()
	if m.ImportedTo != nil {
		fmt.Printf("Imported: %s v%s at %s\n", m.ImportedTo.Arch, m.ImportedTo.ArchVersion, m.ImportedTo.ImportedAt)
	}
	fmt.Println()

	// ── Groups ──────────────────────────────────────────────────────────────
	fmt.Printf("Groups (%d):\n", len(m.Groups))
	for _, slug := range m.Groups {
		configKey := filepath.Join("groups", slug, "config.json")
		configData, ok := b.Files[configKey]
		if !ok {
			fmt.Printf("  %-30s  (missing config)\n", slug)
			continue
		}
		var cfg groupConfigSlim
		_ = json.Unmarshal(configData, &cfg)

		// Parse arch-specific fields for symlink target
		var arch archNanoclawSlim
		if len(cfg.ArchNanoclaw) > 0 {
			_ = json.Unmarshal(cfg.ArchNanoclaw, &arch)
		}

		tag := ""
		if cfg.IsMain {
			tag = " [main]"
		}
		if cfg.JID == "" {
			tag += " [no jid]"
		}

		// Count files for this group
		prefix := "groups/" + slug + "/"
		fileCount := 0
		for path := range b.Files {
			if strings.HasPrefix(path, prefix) && !strings.HasSuffix(path, "/config.json") {
				fileCount++
			}
		}

		name := cfg.Name
		if name == "" {
			name = slug
		}

		if arch.SymlinkTarget != "" {
			fmt.Printf("  %-30s  %s%s  → %s\n", slug, name, tag, arch.SymlinkTarget)
		} else {
			fmt.Printf("  %-30s  %s%s  (%d file(s))\n", slug, name, tag, fileCount)
		}
	}
	fmt.Println()

	// ── Tasks ───────────────────────────────────────────────────────────────
	if taskData, ok := b.Files["tasks.json"]; ok {
		var tasks []map[string]interface{}
		if json.Unmarshal(taskData, &tasks) == nil && len(tasks) > 0 {
			fmt.Printf("Tasks (%d):\n", len(tasks))
			for _, t := range tasks {
				id, _ := t["id"].(string)
				stype, _ := t["schedule_type"].(string)
				sval, _ := t["schedule_value"].(string)
				groupSlug, _ := t["group_slug"].(string)
				active, _ := t["active"].(bool)
				activeStr := ""
				if !active {
					activeStr = " [paused]"
				}
				fmt.Printf("  %-20s  %-10s  %s  → %s%s\n", id, stype, sval, groupSlug, activeStr)
			}
			fmt.Println()
		}
	}

	// ── Sessions ─────────────────────────────────────────────────────────────
	groupSet := map[string]bool{}
	for _, s := range m.Groups {
		groupSet[s] = true
	}

	sessionSlugs := map[string]int{}
	for path := range b.Files {
		if !strings.HasPrefix(path, "sessions/") {
			continue
		}
		rest := strings.TrimPrefix(path, "sessions/")
		idx := strings.Index(rest, "/")
		if idx > 0 {
			sessionSlugs[rest[:idx]]++
		}
	}
	if len(sessionSlugs) > 0 {
		slugList := make([]string, 0, len(sessionSlugs))
		for s := range sessionSlugs {
			slugList = append(slugList, s)
		}
		sort.Strings(slugList)
		fmt.Printf("Sessions (%d group(s)):\n", len(slugList))
		for _, s := range slugList {
			orphanTag := ""
			if !groupSet[s] {
				orphanTag = "  ⚠ not in groups"
			}
			fmt.Printf("  %-30s  %d file(s)%s\n", s, sessionSlugs[s], orphanTag)
		}
		fmt.Println()
	}

	// ── Warnings ─────────────────────────────────────────────────────────────
	if len(m.Warnings) > 0 {
		fmt.Printf("Warnings (%d):\n", len(m.Warnings))
		for _, w := range m.Warnings {
			fmt.Printf("  ⚠  %s\n", w)
		}
		fmt.Println()
	}

	// ── Size ─────────────────────────────────────────────────────────────────
	totalBytes := 0
	for _, data := range b.Files {
		totalBytes += len(data)
	}
	fmt.Printf("Bundle size: %s  (%d files)\n", humanBytes(totalBytes), len(b.Files))

	return nil
}

func humanBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
