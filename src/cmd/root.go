package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "molt",
	Short: "Migrate claw agent installations between architectures",
	Long: `molt — portable migration for claw agent architectures.

Move your agents, groups, memory, skills, and config between
NanoClaw, OpenClaw, ZeptoClaw, PicoClaw, and others.

Examples:
  molt export ~/nanoclaw-install --out my-agents.molt
  molt import my-agents.molt ~/new-install --arch zepto
  molt ~/old-install ~/new-install --arch zepto`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 2 {
			// Combined export+import shorthand: molt <source> <dest> --arch <name>
			return runCombined(cmd, args[0], args[1])
		}
		return cmd.Help()
	},
}

var (
	flagArch   string
	flagRename []string
	flagDryRun bool
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagArch, "arch", "", "Target architecture (e.g. nanoclaw, zepto, openclaw, pico)")
	rootCmd.PersistentFlags().StringArrayVar(&flagRename, "rename", nil, "Rename group slug on import: --rename old=new (repeatable)")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "Show what would happen without making changes")

	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(archsCmd)
}

func runCombined(cmd *cobra.Command, source, dest string) error {
	// Detect source arch (export side).
	srcArch, err := detectOrFlagArch(source)
	if err != nil {
		return fmt.Errorf(
			"--arch is required (auto-detect from source failed: %v)\n\nRe-run with:\n  molt %s %s --arch <nanoclaw|zepto|openclaw|pico>",
			err, source, dest)
	}

	renames, err := parseRenames(flagRename)
	if err != nil {
		return err
	}

	if flagDryRun {
		fmt.Printf("dry-run: would export %s (arch: %s) → import → %s\n", source, srcArch, dest)
		for old, newSlug := range renames {
			fmt.Printf("  rename: %s → %s\n", old, newSlug)
		}
		return nil
	}

	// Export to a temp bundle.
	srcDriver, err := locateDriver(srcArch, source)
	if err != nil {
		return err
	}
	fmt.Printf("Exporting %s (arch: %s)...\n", source, srcArch)
	b, err := srcDriver.Export(source, nil)
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	tmp, err := os.CreateTemp("", "molt-*.molt")
	if err != nil {
		return fmt.Errorf("failed to create temp bundle: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := b.SaveTo(tmpPath); err != nil {
		return fmt.Errorf("failed to write temp bundle: %w", err)
	}

	// Detect dest arch — fall back to source arch if dest is empty/new.
	destArch := srcArch
	if detected, err := detectOrFlagArch(dest); err == nil {
		destArch = detected
	} else if flagArch != "" {
		destArch = flagArch
	}

	destDriver, err := locateDriver(destArch, dest)
	if err != nil {
		return err
	}

	fmt.Printf("Importing → %s (arch: %s)...\n", dest, destArch)
	if err := destDriver.Import(tmpPath, dest, renames, nil); err != nil {
		return err
	}

	// Print export warnings.
	for _, w := range b.Manifest.Warnings {
		fmt.Printf("⚠  %s\n", w)
	}

	return nil
}
