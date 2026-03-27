// SPDX-License-Identifier: AGPL-3.0-or-later
package dest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type sshAdapter struct {
	host string
	path string
}

func (a *sshAdapter) Put(name string, r io.Reader) error {
	// Write reader to a temp file so rsync can read it
	tmp, err := os.CreateTemp("", "molt-ssh-*.molt")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot buffer bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	remote := fmt.Sprintf("%s:%s/%s", a.host, a.path, name)
	cmd := exec.Command("rsync", "--mkpath", "-e", "ssh", tmpPath, remote)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync put failed: %w\n%s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (a *sshAdapter) Get(name string, w io.Writer) error {
	tmp, err := os.CreateTemp("", "molt-ssh-*.molt")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	remote := fmt.Sprintf("%s:%s/%s", a.host, a.path, name)
	cmd := exec.Command("rsync", "-e", "ssh", remote, tmpPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync get failed: %w\n%s", err, bytes.TrimSpace(out))
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func (a *sshAdapter) List() ([]BundleEntry, error) {
	remote := fmt.Sprintf("%s:%s", a.host, a.path)
	cmd := exec.Command("ssh", a.host, "ls", a.path)
	out, err := cmd.Output()
	if err != nil {
		// Empty remote dir returns exit code 1 on some systems
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("ssh list failed for %s: %w", remote, err)
	}

	var result []BundleEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, ".molt") {
			continue
		}
		_, bundleType, baseHash, ts, parseErr := ParseBundleName(line)
		if parseErr != nil {
			continue
		}
		result = append(result, BundleEntry{
			Name:      line,
			Timestamp: ts,
			Type:      bundleType,
			BaseHash:  baseHash,
		})
	}
	return result, nil
}
