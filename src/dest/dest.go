// SPDX-License-Identifier: AGPL-3.0-or-later
// Package dest implements destination adapters for molt sync.
package dest

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// BundleEntry describes a bundle stored at a destination.
type BundleEntry struct {
	Name      string
	Timestamp time.Time
	Type      string // "full" or "delta"
	Size      int64
	BaseHash  string // non-empty for delta bundles
}

// Adapter is the interface all destination backends implement.
type Adapter interface {
	Put(name string, r io.Reader) error
	Get(name string, w io.Writer) error
	List() ([]BundleEntry, error)
}

// Parse creates an Adapter from a destination URI.
func Parse(uri string) (Adapter, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid destination URI %q: %w", uri, err)
	}
	switch u.Scheme {
	case "file":
		dir := u.Host + u.Path
		if dir == "" {
			dir = u.Path
		}
		return &fileAdapter{dir: dir}, nil
	case "ssh":
		host := u.Host
		path := u.Path
		return &sshAdapter{host: host, path: path}, nil
	case "s3":
		return nil, fmt.Errorf("s3:// destination not yet implemented (planned for v1.0)")
	default:
		return nil, fmt.Errorf("unsupported destination scheme %q (supported: file://, ssh://)", u.Scheme)
	}
}

// BundleName formats a bundle filename per the molt spec:
//
//	<arch>-<timestamp>-full.molt
//	<arch>-<timestamp>-delta-<baseHash8>.molt
func BundleName(arch string, ts time.Time, bundleType, baseHash string) string {
	timestamp := ts.UTC().Format("20060102T150405Z")
	if bundleType == "delta" {
		return fmt.Sprintf("%s-%s-delta-%s.molt", arch, timestamp, baseHash)
	}
	return fmt.Sprintf("%s-%s-full.molt", arch, timestamp)
}

// ParseBundleName parses a bundle filename back into its components.
// Returns an error if the name does not match the expected format.
func ParseBundleName(name string) (arch, bundleType, baseHash string, ts time.Time, err error) {
	// Strip .molt suffix
	base := strings.TrimSuffix(name, ".molt")
	if base == name {
		err = fmt.Errorf("not a .molt file: %q", name)
		return
	}

	// Try delta: <arch>-<ts>-delta-<hash>
	// Try full:  <arch>-<ts>-full
	// The timestamp is YYYYMMDDTHHmmssZ (16 chars)
	parts := strings.Split(base, "-")
	if len(parts) < 3 {
		err = fmt.Errorf("cannot parse bundle name %q", name)
		return
	}

	// Find the timestamp segment (matches YYYYMMDDTHHmmssZ)
	tsIdx := -1
	for i, p := range parts {
		if len(p) == 16 && strings.Contains(p, "T") {
			tsIdx = i
			break
		}
	}
	if tsIdx < 1 {
		err = fmt.Errorf("cannot locate timestamp in bundle name %q", name)
		return
	}

	arch = strings.Join(parts[:tsIdx], "-")
	ts, err = time.Parse("20060102T150405Z", parts[tsIdx])
	if err != nil {
		err = fmt.Errorf("invalid timestamp in bundle name %q: %w", name, err)
		return
	}

	rest := parts[tsIdx+1:]
	if len(rest) == 1 && rest[0] == "full" {
		bundleType = "full"
	} else if len(rest) >= 2 && rest[0] == "delta" {
		bundleType = "delta"
		baseHash = strings.Join(rest[1:], "-")
	} else {
		err = fmt.Errorf("unrecognised bundle type in %q", name)
	}
	return
}
