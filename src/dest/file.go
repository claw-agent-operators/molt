// SPDX-License-Identifier: AGPL-3.0-or-later
package dest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type fileAdapter struct {
	dir string
}

func (a *fileAdapter) Put(name string, r io.Reader) error {
	if err := os.MkdirAll(a.dir, 0755); err != nil {
		return fmt.Errorf("cannot create destination directory %s: %w", a.dir, err)
	}
	tmp := filepath.Join(a.dir, name+".tmp")
	dst := filepath.Join(a.dir, name)

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write failed: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (a *fileAdapter) Get(name string, w io.Writer) error {
	f, err := os.Open(filepath.Join(a.dir, name))
	if err != nil {
		return fmt.Errorf("cannot open bundle %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func (a *fileAdapter) Delete(name string) error {
	return os.Remove(filepath.Join(a.dir, name))
}

func (a *fileAdapter) List() ([]BundleEntry, error) {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list destination %s: %w", a.dir, err)
	}

	var result []BundleEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".molt") {
			continue
		}
		arch, bundleType, baseHash, ts, parseErr := ParseBundleName(e.Name())
		if parseErr != nil {
			continue // skip files that don't match the naming scheme
		}
		info, err := e.Info()
		size := int64(0)
		if err == nil {
			size = info.Size()
		}
		result = append(result, BundleEntry{
			Name:      e.Name(),
			Timestamp: ts,
			Type:      bundleType,
			Size:      size,
			BaseHash:  baseHash,
			// arch is embedded in name; store in Name, accessible via ParseBundleName
		})
		_ = arch
	}
	return result, nil
}
