// SPDX-License-Identifier: AGPL-3.0-or-later
package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const stateFileName = ".molt-sync-state.json"

// BundleEntry is a record of one stored bundle in the sync state.
type BundleEntry struct {
	Name      string `json:"name"`
	Timestamp string `json:"timestamp"`      // RFC 3339
	Type      string `json:"type"`           // "full" or "delta"
	Hash      string `json:"hash,omitempty"` // sha256[:8] for full bundles
	Base      string `json:"base,omitempty"` // base hash for delta bundles
}

// SyncState is persisted after each successful export run.
type SyncState struct {
	LastSyncAt string        `json:"last_sync_at"`
	LastFullAt string        `json:"last_full_at"`
	LastBundle string        `json:"last_bundle"`
	BaseHash   string        `json:"base_hash"`
	Bundles    []BundleEntry `json:"bundles"`
}

// LoadState reads the sync state for a source directory.
// Returns an empty state (not an error) if the file doesn't exist.
func LoadState(sourceDir string) (*SyncState, error) {
	data, err := os.ReadFile(filepath.Join(sourceDir, stateFileName))
	if os.IsNotExist(err) {
		return &SyncState{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s SyncState
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupted state — fall back to empty (will trigger full export)
		return &SyncState{}, nil
	}
	return &s, nil
}

// SaveState writes the sync state atomically.
func SaveState(sourceDir string, s *SyncState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dst := filepath.Join(sourceDir, stateFileName)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
