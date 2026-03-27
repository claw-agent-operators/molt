// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kenbolton/molt/src/dest"
	moltsync "github.com/kenbolton/molt/src/sync"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Scheduled backup of a claw installation to a destination",
	Long: `molt sync manages a scheduled backup daemon that exports .molt bundles
to a configurable destination on a cron or interval schedule.

The first export is always a full bundle; subsequent exports are delta bundles
containing only what changed since the last run.

Subcommands:
  init    Write .molt-sync.json with defaults
  start   Launch the background daemon
  stop    Stop the daemon gracefully
  status  Show daemon state, last run, next run, bundle count
  run     Trigger an immediate sync (foreground)
  list    List all saved bundles at the destination`,
}

// --- init ---

var (
	syncInitArch      string
	syncInitSourceDir string
	syncInitForce     bool
	syncInitSchedule  string
	syncInitFullEvery string
)

var syncInitCmd = &cobra.Command{
	Use:   "init <destination>",
	Short: "Write .molt-sync.json with defaults",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		destination := args[0]
		sourceDir := syncInitSourceDir
		if sourceDir == "" {
			var err error
			sourceDir, err = os.Getwd()
			if err != nil {
				return err
			}
		}

		cfgPath := sourceDir + "/.molt-sync.json"
		if !syncInitForce {
			if _, err := os.Stat(cfgPath); err == nil {
				return fmt.Errorf("%s already exists — use --force to overwrite", cfgPath)
			}
		}

		arch := syncInitArch
		if arch == "" {
			detected, err := detectOrFlagArch(sourceDir)
			if err != nil {
				return fmt.Errorf("cannot auto-detect arch: %w\nUse --arch to specify", err)
			}
			arch = detected
		}

		cfg := moltsync.Defaults()
		cfg.Destination = destination
		cfg.Arch = arch
		cfg.SourceDir = sourceDir
		if syncInitSchedule != "" {
			cfg.Schedule = syncInitSchedule
		}
		if syncInitFullEvery != "" {
			cfg.FullEvery = syncInitFullEvery
		}

		if err := moltsync.Save(sourceDir, &cfg); err != nil {
			return err
		}

		fmt.Printf("✓ Written %s\n\n", cfgPath)
		fmt.Printf("Next steps:\n")
		fmt.Printf("  molt sync run        # test a manual sync\n")
		fmt.Printf("  molt sync start      # launch the background daemon\n")
		fmt.Printf("  molt sync status     # check daemon state\n")
		return nil
	},
}

// --- start ---

var syncStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Launch the sync daemon in the background",
	RunE: func(_ *cobra.Command, _ []string) error {
		sourceDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg, err := moltsync.Load(sourceDir)
		if err != nil {
			return err
		}
		execPath, err := os.Executable()
		if err != nil {
			return err
		}
		if err := moltsync.Start(execPath, cfg); err != nil {
			return err
		}
		fmt.Println("Daemon started.")
		return nil
	},
}

// --- stop ---

var syncStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the sync daemon",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := moltsync.Stop(); err != nil {
			return err
		}
		fmt.Println("Daemon stopped.")
		return nil
	},
}

// --- status ---

var syncStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon state, last run, next run, bundle count",
	RunE: func(_ *cobra.Command, _ []string) error {
		sourceDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg, cfgErr := moltsync.Load(sourceDir)

		running, pid := moltsync.IsRunning()
		if running {
			fmt.Printf("Daemon:      running (pid %d)\n", pid)
		} else {
			fmt.Println("Daemon:      stopped")
		}

		if cfgErr != nil {
			fmt.Println("Config:      not found — run: molt sync init <destination>")
			return nil
		}

		fmt.Printf("Destination: %s\n", cfg.Destination)
		fmt.Printf("Schedule:    %s\n", cfg.Schedule)

		if next, err := moltsync.NextTick(cfg.Schedule); err == nil {
			fmt.Printf("             (next run in %s)\n", time.Until(next).Round(time.Second))
		}

		state, stateErr := moltsync.LoadState(sourceDir)
		if stateErr != nil || state.LastSyncAt == "" {
			fmt.Println("Last sync:   never")
			return nil
		}

		fmt.Printf("Last sync:   %s\n", state.LastSyncAt)
		if state.LastFullAt != "" {
			fmt.Printf("Last full:   %s\n", state.LastFullAt)
		}

		fullCount, deltaCount := 0, 0
		for _, e := range state.Bundles {
			if e.Type == "full" {
				fullCount++
			} else {
				deltaCount++
			}
		}
		fmt.Printf("Bundles:     %d stored (%d full, %d delta)\n", len(state.Bundles), fullCount, deltaCount)
		return nil
	},
}

// --- run ---

var syncLoopFlag bool

var syncRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Trigger an immediate sync (foreground)",
	RunE: func(_ *cobra.Command, _ []string) error {
		sourceDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg, err := moltsync.Load(sourceDir)
		if err != nil {
			return err
		}

		// Internal: --loop means we've been re-exec'd as a daemon
		if syncLoopFlag {
			moltsync.RunLoop(cfg)
			return nil
		}

		adapter, err := dest.Parse(cfg.Destination)
		if err != nil {
			return err
		}
		state, err := moltsync.LoadState(sourceDir)
		if err != nil {
			return err
		}

		fmt.Printf("Syncing %s → %s...\n", sourceDir, cfg.Destination)
		newState, name, err := moltsync.RunOnce(cfg, state, adapter)
		if err != nil {
			return fmt.Errorf("sync failed: %w", err)
		}
		if err := moltsync.SaveState(sourceDir, newState); err != nil {
			return fmt.Errorf("cannot save state: %w", err)
		}
		fmt.Printf("✓ %s\n", name)
		return nil
	},
}

// --- list ---

var syncListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved bundles at the destination",
	RunE: func(_ *cobra.Command, _ []string) error {
		sourceDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg, err := moltsync.Load(sourceDir)
		if err != nil {
			return err
		}
		adapter, err := dest.Parse(cfg.Destination)
		if err != nil {
			return err
		}
		entries, err := adapter.List()
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Println("No bundles found at destination.")
			return nil
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Timestamp.Before(entries[j].Timestamp)
		})
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tTYPE\tTIMESTAMP\tSIZE")
		for _, e := range entries {
			sizeStr := "-"
			if e.Size > 0 {
				sizeStr = humanBytes(int(e.Size))
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				e.Name, e.Type, e.Timestamp.Format(time.RFC3339), sizeStr)
		}
		return w.Flush()
	},
}

func init() {
	syncInitCmd.Flags().StringVar(&syncInitArch, "arch", "", "Architecture (auto-detected if omitted)")
	syncInitCmd.Flags().StringVar(&syncInitSourceDir, "source", "", "Source directory (default: current directory)")
	syncInitCmd.Flags().BoolVar(&syncInitForce, "force", false, "Overwrite existing config")
	syncInitCmd.Flags().StringVar(&syncInitSchedule, "schedule", "", "Cron expression or interval (default: \"0 * * * *\")")
	syncInitCmd.Flags().StringVar(&syncInitFullEvery, "full-every", "", "How often to write a full bundle (default: \"7d\")")

	syncRunCmd.Flags().BoolVar(&syncLoopFlag, "loop", false, "")
	_ = syncRunCmd.Flags().MarkHidden("loop")

	syncCmd.AddCommand(syncInitCmd, syncStartCmd, syncStopCmd, syncStatusCmd, syncRunCmd, syncListCmd)
}
