// SPDX-License-Identifier: AGPL-3.0-or-later
package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/kenbolton/molt/src/dest"
	"github.com/kenbolton/molt/src/driver"
)

// RunOnce performs one sync run (full or delta), uploads the bundle, updates
// state, and prunes old bundles per the retention policy.
// The caller is responsible for persisting the returned state.
func RunOnce(cfg *SyncConfig, state *SyncState, adapter dest.Adapter) (*SyncState, string, error) {
	isDelta := IsDeltaRun(state, cfg)
	bundleType := "full"
	since := ""
	if isDelta {
		bundleType = "delta"
		since = state.LastSyncAt
	}

	// Locate driver
	d, err := driver.Locate(cfg.Arch, cfg.SourceDir)
	if err != nil {
		return nil, "", fmt.Errorf("driver not found: %w", err)
	}

	// Export
	b, _, err := d.Export(cfg.SourceDir, nil, nil, since)
	if err != nil {
		return nil, "", fmt.Errorf("export failed: %w", err)
	}

	// Set manifest delta fields
	now := time.Now().UTC()
	b.Manifest.BundleType = bundleType
	if isDelta {
		b.Manifest.BaseBundle = state.BaseHash
		b.Manifest.Since = since
	}

	// Save to temp file and compute hash
	tmp, err := os.CreateTemp("", "molt-sync-*.molt")
	if err != nil {
		return nil, "", fmt.Errorf("cannot create temp bundle: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := b.SaveTo(tmpPath); err != nil {
		return nil, "", fmt.Errorf("cannot write bundle: %w", err)
	}

	// Compute SHA-256 hash of bundle bytes (first 8 hex chars)
	hash8, err := FileHash8(tmpPath)
	if err != nil {
		return nil, "", fmt.Errorf("cannot hash bundle: %w", err)
	}

	// Determine base hash for naming
	baseHash := state.BaseHash
	if bundleType == "full" {
		baseHash = hash8
	}

	name := dest.BundleName(d.Arch, now, bundleType, baseHash)

	// Upload
	f, err := os.Open(tmpPath)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read temp bundle: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := adapter.Put(name, f); err != nil {
		return nil, "", fmt.Errorf("upload failed: %w", err)
	}

	// Build new state
	newState := &SyncState{
		LastSyncAt: now.Format(time.RFC3339),
		LastBundle: name,
		Bundles:    state.Bundles,
	}
	if bundleType == "full" {
		newState.LastFullAt = now.Format(time.RFC3339)
		newState.BaseHash = hash8
	} else {
		newState.LastFullAt = state.LastFullAt
		newState.BaseHash = state.BaseHash
	}

	entry := BundleEntry{
		Name:      name,
		Timestamp: now.Format(time.RFC3339),
		Type:      bundleType,
	}
	if bundleType == "full" {
		entry.Hash = hash8
	} else {
		entry.Base = state.BaseHash
	}
	newState.Bundles = append(newState.Bundles, entry)

	// Prune per retention policy (best-effort: log but don't fail)
	newState.Bundles = pruneAndDeleteBundles(newState.Bundles, cfg.Retention, adapter)

	return newState, name, nil
}

// FileHash8 returns the first 8 hex characters of the SHA-256 of a file.
func FileHash8(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:8], nil
}

// pruneAndDeleteBundles enforces retention policy. It returns the surviving entries.
func pruneAndDeleteBundles(entries []BundleEntry, ret RetentionConfig, adapter dest.Adapter) []BundleEntry {
	if ret.KeepBundles <= 0 {
		return entries
	}

	// Sort oldest first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp < entries[j].Timestamp
	})

	for len(entries) > ret.KeepBundles {
		// Count full bundles remaining (excluding the candidate for deletion)
		candidate := entries[0]
		if candidate.Type == "full" {
			fullCount := 0
			for _, e := range entries {
				if e.Type == "full" {
					fullCount++
				}
			}
			if fullCount <= ret.KeepFull {
				// Protected: cannot remove this full bundle — skip past it
				// find the oldest non-protected entry
				removed := false
				for i := 1; i < len(entries); i++ {
					if entries[i].Type != "full" {
						_ = adapter.List // best-effort delete — ignore errors
						_ = deleteBundle(adapter, entries[i].Name)
						entries = append(entries[:i], entries[i+1:]...)
						removed = true
						break
					}
				}
				if !removed {
					break // nothing to prune
				}
				continue
			}
		}
		_ = deleteBundle(adapter, candidate.Name)
		entries = entries[1:]
	}

	return entries
}

func deleteBundle(adapter dest.Adapter, name string) error {
	// Adapter interface doesn't expose Delete — use a type assertion for file adapters.
	// For now this is best-effort: if the adapter doesn't support deletion, we skip.
	type deleter interface {
		Delete(name string) error
	}
	if d, ok := adapter.(deleter); ok {
		return d.Delete(name)
	}
	return nil
}
