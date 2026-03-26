// SPDX-License-Identifier: AGPL-3.0-or-later
// Package driver implements the molt driver protocol.
// Drivers are standalone binaries (molt-driver-<arch>) that communicate
// via newline-delimited JSON on stdin/stdout.
package driver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kenbolton/molt/src/bundle"
)

// Driver is a located and version-verified arch driver.
type Driver struct {
	Arch           string
	ArchVersion    string
	DriverVersion  string
	DriverType     string // "local" or "remote"
	RequiresConfig []string
	Path           string
}

// FindAll locates all molt-driver-* binaries in $PATH and ~/.molt/drivers/.
func FindAll() ([]*Driver, error) {
	var drivers []*Driver
	seen := map[string]bool{}

	for _, dir := range searchPaths() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "molt-driver-") {
				continue
			}
			arch := strings.TrimPrefix(e.Name(), "molt-driver-")
			fullPath := filepath.Join(dir, e.Name())
			if seen[arch] {
				continue
			}
			seen[arch] = true
			d, err := probeDriver(arch, fullPath)
			if err != nil {
				continue
			}
			drivers = append(drivers, d)
		}
	}
	return drivers, nil
}

// Locate finds the driver for a specific arch.
// Pass sourceDir to allow the driver to detect the installed arch version.
func Locate(arch string, sourceDir ...string) (*Driver, error) {
	name := "molt-driver-" + arch
	src := ""
	if len(sourceDir) > 0 {
		src = sourceDir[0]
	}

	// Check ~/.molt/drivers/ and $PATH
	for _, dir := range searchPaths() {
		fullPath := filepath.Join(dir, name)
		if _, err := os.Stat(fullPath); err == nil {
			return probeDriver(arch, fullPath, src)
		}
	}
	// Also try exec.LookPath
	if path, err := exec.LookPath(name); err == nil {
		return probeDriver(arch, path, src)
	}

	return nil, fmt.Errorf(
		"driver not found for arch %q\n\nInstall molt-driver-%s to $PATH or ~/.molt/drivers/\n\nInstalled drivers:\n  molt archs",
		arch, arch,
	)
}

// DetectArch probes all installed local drivers and returns the best match.
func DetectArch(sourceDir string) (string, error) {
	drivers, err := FindAll()
	if err != nil {
		return "", err
	}

	type candidate struct {
		arch       string
		confidence float64
	}
	var best candidate

	for _, d := range drivers {
		if d.DriverType == "remote" {
			continue // remote drivers can't auto-detect from path
		}
		c, err := probeConfidence(d, sourceDir)
		if err != nil {
			continue
		}
		if c > best.confidence {
			best = candidate{arch: d.Arch, confidence: c}
		}
	}

	if best.confidence == 0 {
		return "", fmt.Errorf(
			"could not detect arch for %q\n\nUse --arch to specify: --arch <nanoclaw|zepto|openclaw|pico>",
			sourceDir,
		)
	}
	return best.arch, nil
}

// Export runs the export protocol: spawns the driver, streams output,
// assembles and returns a Bundle.
func (d *Driver) Export(sourceDir string, config map[string]interface{}, exclude []string) (*bundle.Bundle, []string, error) {
	req := map[string]interface{}{
		"type":       "export_request",
		"source_dir": sourceDir,
		"config":     config,
	}
	reqJSON, _ := json.Marshal(req)

	cmd := exec.Command(d.Path)
	cmd.Stdin = strings.NewReader(string(reqJSON) + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start driver: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start driver: %w", err)
	}

	assembler := bundle.NewAssembler(d.Arch, d.ArchVersion, exclude)
	scanner := bufio.NewScanner(stdout)
	// 200MB buffer — group messages can be large (many conversation files)
	scanner.Buffer(make([]byte, 1024*1024), 200*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, nil, fmt.Errorf("driver sent invalid JSON: %w", err)
		}
		done, err := assembler.Feed(msg)
		if err != nil {
			_ = cmd.Wait()
			return nil, nil, err
		}
		if done {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading driver output: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, nil, fmt.Errorf("driver exited with error: %w", err)
	}

	return assembler.Bundle(), assembler.Excluded(), nil
}

// Import runs the import protocol against the driver.
func (d *Driver) Import(bundlePath, destDir string, renames map[string]string, config map[string]interface{}) error {
	b, err := bundle.Load(bundlePath)
	if err != nil {
		return err
	}

	// Apply renames to bundle before sending
	bundleData, _ := json.Marshal(b)

	req := map[string]interface{}{
		"type":     "import_request",
		"dest_dir": destDir,
		"config":   config,
		"renames":  renames,
		"bundle":   json.RawMessage(bundleData),
	}
	reqJSON, _ := json.Marshal(req)

	cmd := exec.Command(d.Path)
	stdin := strings.NewReader(string(reqJSON) + "\n")
	cmd.Stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to start driver: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start driver: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "progress":
			fmt.Println(" ", msg["message"])
		case "import_complete":
			printWarnings(msg)
			return cmd.Wait()
		case "collision":
			slug, _ := msg["slug"].(string)
			return fmt.Errorf(
				"agent slug collision — %q already exists in dest\n\nRe-run with:\n  molt import %s %s --arch %s --rename %s=%s-imported",
				slug, bundlePath, destDir, d.Arch, slug, slug,
			)
		case "error":
			code, _ := msg["code"].(string)
			message, _ := msg["message"].(string)
			return fmt.Errorf("driver error [%s]: %s", code, message)
		}
	}

	return cmd.Wait()
}

// probeDriver calls the driver's version endpoint, optionally passing sourceDir
// so the driver can detect the installed arch version from package.json.
func probeDriver(arch, path string, sourceDir ...string) (*Driver, error) {
	req := map[string]interface{}{"type": "version_request"}
	if len(sourceDir) > 0 && sourceDir[0] != "" {
		req["source_dir"] = sourceDir[0]
	}
	reqJSON, _ := json.Marshal(req)
	cmd := exec.Command(path)
	cmd.Stdin = strings.NewReader(string(reqJSON) + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("driver %s version check failed: %w", path, err)
	}

	var resp struct {
		Type           string   `json:"type"`
		Arch           string   `json:"arch"`
		ArchVersion    string   `json:"arch_version"`
		DriverVersion  string   `json:"driver_version"`
		DriverType     string   `json:"driver_type"`
		RequiresConfig []string `json:"requires_config"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &resp); err == nil && resp.Type == "version_response" {
			break
		}
	}
	if resp.Arch == "" {
		return nil, fmt.Errorf("driver %s returned no version_response", path)
	}
	if resp.DriverType == "" {
		resp.DriverType = "local"
	}
	return &Driver{
		Arch:           resp.Arch,
		ArchVersion:    resp.ArchVersion,
		DriverVersion:  resp.DriverVersion,
		DriverType:     resp.DriverType,
		RequiresConfig: resp.RequiresConfig,
		Path:           path,
	}, nil
}

// probeConfidence asks a driver how confident it is about a source directory.
func probeConfidence(d *Driver, sourceDir string) (float64, error) {
	req, _ := json.Marshal(map[string]string{
		"type":       "probe_request",
		"source_dir": sourceDir,
	})
	cmd := exec.Command(d.Path)
	cmd.Stdin = strings.NewReader(string(req) + "\n")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var resp struct {
		Type       string  `json:"type"`
		Confidence float64 `json:"confidence"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &resp); err == nil && resp.Type == "probe_response" {
			return resp.Confidence, nil
		}
	}
	return 0, nil
}

func searchPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".molt", "drivers"))
	}
	paths = append(paths, filepath.SplitList(os.Getenv("PATH"))...)
	return paths
}

func printWarnings(msg map[string]interface{}) {
	rawWarnings, _ := msg["warnings"].([]interface{})
	for _, w := range rawWarnings {
		if s, ok := w.(string); ok {
			fmt.Printf("⚠  %s\n", s)
		}
	}
}
