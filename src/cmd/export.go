// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export <source>",
	Short: "Export a claw installation to a .molt bundle",
	Long: `Export an existing claw installation to a portable .molt bundle.

The source architecture is auto-detected via installed drivers.
Use --arch to override detection.

Use --include to export only specific groups, or --exclude to drop individual
groups from the bundle. They are mutually exclusive. Excluding a group that is
the target of a symlink group will cause that symlink to warn on import.

Examples:
  molt export ~/nanoclaw-install
  molt export ~/nanoclaw-install --out backup.molt
  molt export ~/nanoclaw-install --arch nanoclaw
  molt export ~/nanoclaw-install --include surf-crew
  molt export ~/nanoclaw-install --exclude kycsitescan --exclude tanglewylde`,
	Args: cobra.ExactArgs(1),
	RunE: runExport,
	ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveFilterDirs
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
}

var exportOut string

func init() {
	exportCmd.Flags().StringVar(&exportOut, "out", "", "Output bundle path (default: <source-basename>.molt)")
}

func runExport(cmd *cobra.Command, args []string) error {
	if len(flagInclude) > 0 && len(flagExclude) > 0 {
		return fmt.Errorf("--include and --exclude are mutually exclusive")
	}

	sourceDir := args[0]

	arch, err := detectOrFlagArch(sourceDir)
	if err != nil {
		return err
	}

	driver, err := locateDriver(arch, sourceDir)
	if err != nil {
		return err
	}

	outPath := exportOut
	if outPath == "" {
		outPath = bundleNameFromSource(sourceDir)
	}

	if flagDryRun {
		fmt.Printf("dry-run: would export %s (arch: %s) → %s\n", sourceDir, arch, outPath)
		for _, slug := range flagExclude {
			fmt.Printf("  would exclude: %s\n", slug)
		}
		for _, slug := range flagInclude {
			fmt.Printf("  would include: %s\n", slug)
		}
		return nil
	}

	fmt.Printf("Exporting %s (arch: %s)...\n", sourceDir, arch)

	b, excluded, err := driver.Export(sourceDir, nil, flagExclude, flagInclude, "")
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}
	for _, slug := range excluded {
		fmt.Printf("  excluded: %s\n", slug)
	}

	if err := b.SaveTo(outPath); err != nil {
		return fmt.Errorf("failed to write bundle: %w", err)
	}

	fmt.Printf("✓ Written to %s\n", outPath)

	// Print any warnings
	for _, w := range b.Manifest.Warnings {
		fmt.Printf("⚠  %s\n", w)
	}

	return nil
}
