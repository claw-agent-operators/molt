// SPDX-License-Identifier: AGPL-3.0-or-later
// Package bundle implements the .molt bundle format.
// A bundle is a gzipped tar archive with a manifest.json and predictable layout.
package bundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const CurrentVersion = "0.1.0"

// Bundle is an in-memory representation of a .molt file.
type Bundle struct {
	Manifest *Manifest
	Files    map[string][]byte // path → contents
}

// Manifest is the bundle's manifest.json.
type Manifest struct {
	MoltVersion string            `json:"molt_version"`
	CreatedAt   string            `json:"created_at"`
	Source      ArchInfo          `json:"source"`
	ImportedTo  *ArchInfo         `json:"imported_to"`
	Groups      []string            `json:"groups"`
	Skills      map[string][]string `json:"skills,omitempty"` // skill name → group slugs
	Warnings    []string            `json:"warnings"`
	Checksums   map[string]string   `json:"checksums,omitempty"`
}

// ArchInfo describes a claw architecture install.
type ArchInfo struct {
	Arch        string `json:"arch"`
	ArchVersion string `json:"arch_version"`
	Hostname    string `json:"hostname,omitempty"`
	ImportedAt  string `json:"imported_at,omitempty"`
}

// New creates an empty bundle with a fresh manifest.
func New(sourceArch, sourceVersion string) *Bundle {
	hostname, _ := os.Hostname()
	return &Bundle{
		Manifest: &Manifest{
			MoltVersion: CurrentVersion,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
			Source: ArchInfo{
				Arch:        sourceArch,
				ArchVersion: sourceVersion,
				Hostname:    hostname,
			},
			Warnings: []string{},
		},
		Files: map[string][]byte{},
	}
}

// Load reads a .molt bundle from disk.
func Load(path string) (*Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open bundle: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("not a valid .molt bundle (gzip): %w", err)
	}
	defer func() { _ = gz.Close() }()

	bundle := &Bundle{Files: map[string][]byte{}}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("corrupt bundle: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		bundle.Files[hdr.Name] = data
	}

	manifestData, ok := bundle.Files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("invalid bundle: missing manifest.json")
	}
	if err := json.Unmarshal(manifestData, &bundle.Manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest.json: %w", err)
	}

	return bundle, nil
}

// SaveTo writes the bundle to disk as a gzipped tar.
func (b *Bundle) SaveTo(path string) error {
	// Re-serialize manifest (may have been upgraded)
	manifestData, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return err
	}
	b.Files["manifest.json"] = manifestData

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for name, data := range b.Files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// SecretsTemplate generates a secrets-template.env from known secret key names.
func (b *Bundle) SecretsTemplate(keys []string, sourceArch string) []byte {
	lines := []string{
		"# molt secrets template",
		"# Fill in before starting target arch",
		fmt.Sprintf("# Exported from %s @ %s", sourceArch, b.Manifest.CreatedAt),
		"",
	}
	for _, k := range keys {
		lines = append(lines, k+"=")
	}
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return []byte(out)
}

// BundleNameFromSource derives a default .molt filename from an installation path.
func BundleNameFromSource(sourceDir string) string {
	base := filepath.Base(filepath.Clean(sourceDir))
	return base + ".molt"
}
