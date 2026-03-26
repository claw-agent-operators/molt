// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"
	"strings"

	"github.com/kenbolton/molt/src/driver"
	"github.com/spf13/cobra"
)

var archsCmd = &cobra.Command{
	Use:   "archs",
	Short: "List installed claw architecture drivers",
	Long: `List molt drivers found in $PATH or ~/.molt/drivers/.

Each claw architecture ships its own molt-driver-<arch> binary.
Install them to enable import/export for that architecture.

Examples:
  molt archs`,
	RunE: runArchs,
}

func runArchs(cmd *cobra.Command, args []string) error {
	drivers, err := driver.FindAll()
	if err != nil {
		return err
	}

	if len(drivers) == 0 {
		fmt.Println("No drivers found.")
		fmt.Println()
		fmt.Println("Install a driver to your PATH or ~/.molt/drivers/:")
		fmt.Println("  molt-driver-nanoclaw   (ships with NanoClaw)")
		fmt.Println("  molt-driver-zepto      (ships with ZeptoClaw)")
		fmt.Println("  molt-driver-openclaw   (ships with OpenClaw)")
		fmt.Println("  molt-driver-pico       (ships with PicoClaw)")
		return nil
	}

	fmt.Printf("%-20s %-12s %-12s %s\n", "ARCH", "ARCH VER", "DRIVER VER", "PATH")
	fmt.Println(strings.Repeat("-", 70))
	for _, d := range drivers {
		fmt.Printf("%-20s %-12s %-12s %s\n", d.Arch, d.ArchVersion, d.DriverVersion, d.Path)
	}
	return nil
}
