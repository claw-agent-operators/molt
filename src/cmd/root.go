// SPDX-License-Identifier: AGPL-3.0-or-later
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
  molt inspect my-agents.molt
  molt import my-agents.molt ~/new-install --arch zepto
  molt ~/old-install ~/new-install --arch zepto`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 2 {
			// Combined export+import shorthand: molt <source> <dest> --arch <name>
			return runCombined(cmd, args[0], args[1])
		}
		return cmd.Help()
	},
	ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) <= 1 {
			return nil, cobra.ShellCompDirectiveFilterDirs
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
}

var (
	flagArch    string
	flagRename  []string
	flagDryRun  bool
	flagExclude []string
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
	rootCmd.PersistentFlags().StringArrayVar(&flagExclude, "exclude", nil, "Exclude group slug from bundle (repeatable)")

	_ = rootCmd.RegisterFlagCompletionFunc("arch", completeArchs)
	_ = rootCmd.RegisterFlagCompletionFunc("rename", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

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
		for _, slug := range flagExclude {
			fmt.Printf("  would exclude: %s\n", slug)
		}
		return nil
	}

	// Export to a temp bundle.
	srcDriver, err := locateDriver(srcArch, source)
	if err != nil {
		return err
	}
	fmt.Printf("Exporting %s (arch: %s)...\n", source, srcArch)
	b, excluded, err := srcDriver.Export(source, nil, flagExclude)
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}
	for _, slug := range excluded {
		fmt.Printf("  excluded: %s\n", slug)
	}

	tmp, err := os.CreateTemp("", "molt-*.molt")
	if err != nil {
		return fmt.Errorf("failed to create temp bundle: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

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
