// SPDX-License-Identifier: AGPL-3.0-or-later
package sync

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kenbolton/molt/src/dest"
)

const pidFileName = "sync.pid"

// PIDFile returns the path to the daemon PID file.
func PIDFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".molt", pidFileName), nil
}

// IsRunning checks whether the sync daemon is currently running.
// Returns (running, pid).
func IsRunning() (bool, int) {
	pidFile, err := PIDFile()
	if err != nil {
		return false, 0
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}
	// On Unix, FindProcess always succeeds; send signal 0 to check existence
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, 0
	}
	return true, pid
}

// writePID writes the current process PID to the PID file.
func writePID() error {
	pidFile, err := PIDFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// removePID removes the PID file.
func removePID() {
	pidFile, _ := PIDFile()
	_ = os.Remove(pidFile)
}

// Start forks the daemon by re-executing the current binary with the hidden
// "--loop" flag. Returns once the child has started.
func Start(execPath string, cfg *SyncConfig) error {
	if running, pid := IsRunning(); running {
		return fmt.Errorf("daemon already running (pid %d)", pid)
	}

	// Ensure PID dir exists
	pidFile, err := PIDFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0755); err != nil {
		return err
	}

	// Re-exec with the internal loop flag
	proc, err := os.StartProcess(execPath, []string{execPath, "sync", "run", "--loop"}, &os.ProcAttr{
		Files: []*os.File{nil, nil, nil},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return fmt.Errorf("cannot start daemon: %w", err)
	}
	_ = proc.Release()
	return nil
}

// Stop sends SIGTERM to the running daemon and waits for it to exit.
func Stop() error {
	running, pid := IsRunning()
	if !running {
		return fmt.Errorf("daemon is not running")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("cannot find daemon process: %w", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("cannot signal daemon: %w", err)
	}

	// Wait up to 30 seconds for the process to exit
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process is gone
			removePID()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not stop within 30 seconds")
}

// RunLoop is the internal daemon loop invoked when the process is started with --loop.
func RunLoop(cfg *SyncConfig) {
	if err := writePID(); err != nil {
		fmt.Fprintf(os.Stderr, "molt sync: cannot write PID file: %v\n", err)
		os.Exit(1)
	}
	defer removePID()

	adapter, err := dest.Parse(cfg.Destination)
	if err != nil {
		fmt.Fprintf(os.Stderr, "molt sync: invalid destination: %v\n", err)
		os.Exit(1)
	}

	state, _ := LoadState(cfg.SourceDir)

	done := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigs
		close(done)
	}()

	for {
		next, err := NextTick(cfg.Schedule)
		if err != nil {
			fmt.Fprintf(os.Stderr, "molt sync: invalid schedule: %v\n", err)
			os.Exit(1)
		}

		select {
		case <-done:
			return
		case <-time.After(time.Until(next)):
		}

		select {
		case <-done:
			return
		default:
		}

		newState, name, err := RunOnce(cfg, state, adapter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "molt sync: run failed: %v\n", err)
			continue
		}

		if err := SaveState(cfg.SourceDir, newState); err != nil {
			fmt.Fprintf(os.Stderr, "molt sync: cannot save state: %v\n", err)
		}
		state = newState
		fmt.Fprintf(os.Stderr, "molt sync: uploaded %s\n", name)
	}
}
