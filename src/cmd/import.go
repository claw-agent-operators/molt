package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import <bundle> <dest>",
	Short: "Import a .molt bundle into a claw installation",
	Long: `Import a .molt bundle into a target claw architecture.

Examples:
  molt import my-agents.molt ~/new-install --arch nanoclaw
  molt import my-agents.molt ~/new-install --arch zepto --rename main=main-old
  molt import my-agents.molt ~/new-install --arch nanoclaw --dry-run`,
	Args: cobra.ExactArgs(2),
	RunE: runImport,
}

func runImport(cmd *cobra.Command, args []string) error {
	bundlePath := args[0]
	destDir := args[1]

	arch, err := detectOrFlagArch(destDir)
	if err != nil {
		return fmt.Errorf("--arch is required (auto-detect from dest failed: %v)\n\nRe-run with:\n  molt import %s %s --arch <nanoclaw|zepto|openclaw|pico>",
			err, bundlePath, destDir)
	}

	renames, err := parseRenames(flagRename)
	if err != nil {
		return err
	}

	driver, err := locateDriver(arch)
	if err != nil {
		return err
	}

	if flagDryRun {
		fmt.Printf("dry-run: would import %s → %s (arch: %s)\n", bundlePath, destDir, arch)
		if len(renames) > 0 {
			for old, newSlug := range renames {
				fmt.Printf("  rename: %s → %s\n", old, newSlug)
			}
		}
		return nil
	}

	fmt.Printf("Importing %s → %s (arch: %s)...\n", bundlePath, destDir, arch)
	return driver.Import(bundlePath, destDir, renames, nil)
}

// parseRenames parses --rename old=new flags into a map.
func parseRenames(flags []string) (map[string]string, error) {
	renames := make(map[string]string)
	for _, r := range flags {
		parts := strings.SplitN(r, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid --rename value %q: must be old=new", r)
		}
		renames[parts[0]] = parts[1]
	}
	return renames, nil
}
