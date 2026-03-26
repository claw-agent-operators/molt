// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"

	"github.com/kenbolton/molt/src/bundle"
	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade <bundle>",
	Short: "Upgrade a .molt bundle to the current format version",
	Long: `Rewrite a .molt bundle to the current format version.

Bundles from older versions of molt are imported best-effort,
but upgrade gives you a clean, validated bundle before importing.

Examples:
  molt upgrade old-agents.molt
  molt upgrade old-agents.molt --out upgraded.molt`,
	Args: cobra.ExactArgs(1),
	RunE: runUpgrade,
}

var upgradeOut string

func init() {
	upgradeCmd.Flags().StringVar(&upgradeOut, "out", "", "Output path (default: overwrites in place)")
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	bundlePath := args[0]
	outPath := upgradeOut
	if outPath == "" {
		outPath = bundlePath
	}

	b, err := bundle.Load(bundlePath)
	if err != nil {
		return fmt.Errorf("failed to load bundle: %w", err)
	}

	if b.Manifest.MoltVersion == bundle.CurrentVersion {
		fmt.Printf("Bundle is already at current version (%s), nothing to do.\n", bundle.CurrentVersion)
		return nil
	}

	fmt.Printf("Upgrading bundle: %s → %s\n", b.Manifest.MoltVersion, bundle.CurrentVersion)

	if flagDryRun {
		fmt.Printf("dry-run: would write upgraded bundle to %s\n", outPath)
		return nil
	}

	return b.SaveTo(outPath)
}
