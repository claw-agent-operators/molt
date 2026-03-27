// SPDX-License-Identifier: AGPL-3.0-or-later
// Package sync implements the molt sync scheduled backup daemon.
package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const configFileName = ".molt-sync.json"

// SyncConfig holds the configuration for molt sync.
type SyncConfig struct {
	Destination string          `json:"destination"`
	Schedule    string          `json:"schedule"`   // cron expression or interval ("1h", "15m")
	FullEvery   string          `json:"full_every"` // "7d", "1h", etc.
	Retention   RetentionConfig `json:"retention"`
	Arch        string          `json:"arch"`
	SourceDir   string          `json:"source_dir"`
}

// RetentionConfig controls how many bundles are kept.
type RetentionConfig struct {
	KeepBundles int `json:"keep_bundles"`
	KeepFull    int `json:"keep_full"`
}

// Defaults returns a SyncConfig populated with default values.
func Defaults() SyncConfig {
	return SyncConfig{
		Schedule:  "0 * * * *",
		FullEvery: "7d",
		Retention: RetentionConfig{
			KeepBundles: 168,
			KeepFull:    4,
		},
	}
}

// Load looks up the sync config for a source directory.
// It checks <sourceDir>/.molt-sync.json first, then ~/.molt/sync.json.
func Load(sourceDir string) (*SyncConfig, error) {
	// 1. Co-located config
	local := filepath.Join(sourceDir, configFileName)
	if cfg, err := readConfig(local); err == nil {
		return cfg, nil
	}

	// 2. Global fallback
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot locate home directory: %w", err)
	}
	global := filepath.Join(home, ".molt", "sync.json")
	if cfg, err := readConfig(global); err == nil {
		return cfg, nil
	}

	return nil, fmt.Errorf("no sync config found — run: molt sync init <destination>")
}

// Save writes the config to <sourceDir>/.molt-sync.json atomically.
func Save(sourceDir string, cfg *SyncConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dst := filepath.Join(sourceDir, configFileName)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("cannot write sync config: %w", err)
	}
	return os.Rename(tmp, dst)
}

func readConfig(path string) (*SyncConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg SyncConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid sync config at %s: %w", path, err)
	}
	return &cfg, nil
}
