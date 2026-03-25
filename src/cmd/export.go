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

Examples:
  molt export ~/nanoclaw-install
  molt export ~/nanoclaw-install --out backup.molt
  molt export ~/nanoclaw-install --arch nanoclaw`,
	Args: cobra.ExactArgs(1),
	RunE: runExport,
}

var exportOut string

func init() {
	exportCmd.Flags().StringVar(&exportOut, "out", "", "Output bundle path (default: <source-basename>.molt)")
}

func runExport(cmd *cobra.Command, args []string) error {
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
		return nil
	}

	fmt.Printf("Exporting %s (arch: %s)...\n", sourceDir, arch)

	b, err := driver.Export(sourceDir, nil)
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
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
