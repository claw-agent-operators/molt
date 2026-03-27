// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/kenbolton/molt/src/bundle"
	"github.com/kenbolton/molt/src/dest"
	moltsync "github.com/kenbolton/molt/src/sync"
)

var (
	restoreAt   string
	restoreFrom string
	restoreTo   string
)

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore from a saved bundle chain",
	Long: `Restore a claw installation from a full+delta bundle chain at a destination.

Finds the latest full bundle at or before --at, layers all delta bundles on top,
and imports the assembled result into the target installation.

Examples:
  molt restore --from file:///backups/nanoclaw --to ~/src/nanoclaw
  molt restore --from s3://bucket/backups --at 2026-03-27T10:30:00Z --dry-run`,
	RunE: runRestore,
}

func init() {
	restoreCmd.Flags().StringVar(&restoreAt, "at", "", "Restore to this point in time (ISO 8601; default: latest)")
	restoreCmd.Flags().StringVar(&restoreFrom, "from", "", "Destination URI to restore from")
	restoreCmd.Flags().StringVar(&restoreTo, "to", "", "Installation directory to restore into")
}

func runRestore(_ *cobra.Command, _ []string) error {
	// Determine cut-off time
	atTime := time.Now()
	if restoreAt != "" {
		var err error
		atTime, err = time.Parse(time.RFC3339, restoreAt)
		if err != nil {
			// Try without Z
			atTime, err = time.ParseInLocation("2006-01-02T15:04:05", restoreAt, time.Local)
			if err != nil {
				return fmt.Errorf("invalid --at timestamp %q: use ISO 8601, e.g. 2026-03-27T10:30:00Z", restoreAt)
			}
		}
	}

	// Resolve destination
	fromURI := restoreFrom
	if fromURI == "" {
		// Try to load from config in current dir
		sourceDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg, err := moltsync.Load(sourceDir)
		if err != nil {
			return fmt.Errorf("--from is required (no sync config found)")
		}
		fromURI = cfg.Destination
	}

	adapter, err := dest.Parse(fromURI)
	if err != nil {
		return err
	}

	// Resolve target directory
	toDir := restoreTo
	if toDir == "" {
		sourceDir, err := os.Getwd()
		if err != nil {
			return err
		}
		toDir = sourceDir
	}

	// List all bundles
	entries, err := adapter.List()
	if err != nil {
		return fmt.Errorf("cannot list destination: %w", err)
	}

	// Find the latest full bundle at or before atTime
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	var baseFull *dest.BundleEntry
	for i := range entries {
		e := &entries[i]
		if e.Type == "full" && !e.Timestamp.After(atTime) {
			baseFull = e
		}
	}
	if baseFull == nil {
		return fmt.Errorf("no full bundle found at or before %s", atTime.Format(time.RFC3339))
	}

	// Parse the hash from the full bundle name
	_, _, _, _, parseErr := dest.ParseBundleName(baseFull.Name)
	if parseErr != nil {
		return fmt.Errorf("cannot parse full bundle name: %w", parseErr)
	}
	// Re-parse to get the hash (base is empty for full bundles; hash comes from BundleEntry)
	// fullHash is computed later by filterDeltasForFull (downloads the bundle)
	_ = baseFull.BaseHash

	// Collect deltas: base matches full bundle hash, timestamp ≤ atTime
	var deltas []dest.BundleEntry
	for _, e := range entries {
		if e.Type == "delta" && !e.Timestamp.After(atTime) {
			// Match by name: delta's BaseHash should match the full bundle's hash
			// The hash is embedded in the delta name: <arch>-<ts>-delta-<hash>.molt
			_, _, deltaBase, _, err := dest.ParseBundleName(e.Name)
			if err != nil {
				continue
			}
			// We need the full bundle's hash. Parse it from the state file if available,
			// or derive by checking all deltas that share the same base.
			// Strategy: find full bundle hash from any matching delta, or use our entry
			_ = deltaBase
			deltas = append(deltas, e)
		}
	}

	// Filter deltas to those whose base matches the selected full bundle
	// We need to know the full bundle's hash. Download the full bundle and compute it,
	// or find it via the state file.
	filteredDeltas, err := filterDeltasForFull(adapter, baseFull, deltas, atTime)
	if err != nil {
		return err
	}

	sort.Slice(filteredDeltas, func(i, j int) bool {
		return filteredDeltas[i].Timestamp.Before(filteredDeltas[j].Timestamp)
	})

	// Print chain
	chain := append([]dest.BundleEntry{*baseFull}, filteredDeltas...)
	fmt.Printf("Restore chain for %s:\n", atTime.Format("2006-01-02T15:04"))
	for _, e := range chain {
		tag := "full"
		if e.Type == "delta" {
			tag = "delta"
		}
		fmt.Printf("  [%s]  %s  (%s)\n", tag, e.Name, e.Timestamp.Format("2006-01-02 15:04"))
	}
	fmt.Printf("  %d bundle(s)\n", len(chain))

	if flagDryRun {
		fmt.Println("  (dry run — no changes made)")
		return nil
	}

	// Download and assemble
	assembled, err := downloadAndAssemble(adapter, baseFull, filteredDeltas)
	if err != nil {
		return fmt.Errorf("assembly failed: %w", err)
	}

	// Save assembled bundle to temp file
	tmp, err := os.CreateTemp("", "molt-restore-*.molt")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := assembled.SaveTo(tmpPath); err != nil {
		return fmt.Errorf("cannot write assembled bundle: %w", err)
	}

	// Detect arch and import
	arch := assembled.Manifest.Source.Arch
	if arch == "" {
		arch, err = detectOrFlagArch(toDir)
		if err != nil {
			return fmt.Errorf("--arch required: %w", err)
		}
	}

	d, err := locateDriver(arch, toDir)
	if err != nil {
		return err
	}

	fmt.Printf("Importing → %s (arch: %s)...\n", toDir, arch)
	return d.Import(tmpPath, toDir, nil, nil)
}

// filterDeltasForFull returns the deltas that belong to the given full bundle.
// It downloads the full bundle to compute its hash, then matches delta names.
func filterDeltasForFull(adapter dest.Adapter, full *dest.BundleEntry, candidates []dest.BundleEntry, _ time.Time) ([]dest.BundleEntry, error) {
	// Download the full bundle to a temp file and compute its hash
	tmp, err := os.CreateTemp("", "molt-full-*.molt")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	f, err := os.OpenFile(tmpPath, os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	if err := adapter.Get(full.Name, f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cannot download full bundle %s: %w", full.Name, err)
	}
	_ = f.Close()

	// Compute hash of the full bundle
	hash8, err := fileHash8(tmpPath)
	if err != nil {
		return nil, err
	}

	var result []dest.BundleEntry
	for _, d := range candidates {
		_, _, deltaBase, _, err := dest.ParseBundleName(d.Name)
		if err != nil {
			continue
		}
		if deltaBase == hash8 {
			result = append(result, d)
		}
	}
	return result, nil
}

// downloadAndAssemble downloads and merges a full bundle with zero or more
// delta bundles, returning the assembled Bundle.
func downloadAndAssemble(adapter dest.Adapter, full *dest.BundleEntry, deltas []dest.BundleEntry) (*bundle.Bundle, error) {
	base, err := downloadBundle(adapter, full.Name)
	if err != nil {
		return nil, err
	}

	for _, delta := range deltas {
		d, err := downloadBundle(adapter, delta.Name)
		if err != nil {
			return nil, err
		}
		// Merge: delta files overwrite base files
		for path, data := range d.Files {
			if path != "manifest.json" {
				base.Files[path] = data
			}
		}
		// Merge groups from delta manifest
		seen := map[string]bool{}
		for _, g := range base.Manifest.Groups {
			seen[g] = true
		}
		for _, g := range d.Manifest.Groups {
			if !seen[g] {
				base.Manifest.Groups = append(base.Manifest.Groups, g)
			}
		}
	}

	// Clear delta-specific fields from the assembled manifest
	base.Manifest.BundleType = "full"
	base.Manifest.BaseBundle = ""
	base.Manifest.Since = ""

	return base, nil
}

func downloadBundle(adapter dest.Adapter, name string) (*bundle.Bundle, error) {
	tmp, err := os.CreateTemp("", "molt-dl-*.molt")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	f, err := os.OpenFile(tmpPath, os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	if err := adapter.Get(name, f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cannot download %s: %w", name, err)
	}
	_ = f.Close()

	return bundle.Load(tmpPath)
}

func fileHash8(path string) (string, error) {
	return moltsync.FileHash8(path)
}
