// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeZeptoClaw_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Isolate from real ~/.zeptoclaw
	t.Setenv("ZEPTOCLAW_DIR", dir)
	score := probeZeptoClaw(dir)
	if score >= 0.5 {
		t.Errorf("empty dir should have low confidence, got %f", score)
	}
}

func TestProbeZeptoClaw_ConfigOnly(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":"0.1.0"}`), 0o644)

	score := probeZeptoClaw(dir)
	if score < 0.5 {
		t.Errorf("config.json alone should give ≥0.5 confidence, got %f", score)
	}
}

func TestProbeZeptoClaw_FullInstall(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":"0.8.2"}`), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "sessions"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "memory"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "cron"), 0o755)

	score := probeZeptoClaw(dir)
	if score < 0.89 {
		t.Errorf("full install should have ≥0.9 confidence, got %f", score)
	}
}

func TestValidateSource_Valid(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":"0.1.0"}`), 0o644)

	if err := validateSource(dir); err != nil {
		t.Errorf("valid source should not error: %v", err)
	}
}

func TestValidateSource_Invalid(t *testing.T) {
	dir := t.TempDir()
	// Isolate from real ~/.zeptoclaw so probe doesn't find real install
	t.Setenv("ZEPTOCLAW_DIR", dir)
	if err := validateSource(dir); err == nil {
		t.Error("empty dir should fail validation")
	}
}

func TestDetectArchVersion_ConfigJSON(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":"0.8.2"}`), 0o644)

	version := detectArchVersion(dir)
	if version != "0.8.2" {
		t.Errorf("expected version 0.8.2, got %q", version)
	}
}

func TestDetectArchVersion_Unknown(t *testing.T) {
	dir := t.TempDir()
	version := detectArchVersion(dir)
	if version != "unknown" {
		t.Errorf("expected 'unknown', got %q", version)
	}
}

func TestFindDataDir_SourceDir(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644)

	got := findDataDir(dir)
	if got != dir {
		t.Errorf("findDataDir with valid source should return source, got %q", got)
	}
}

func TestFindDataDir_EnvVar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEPTOCLAW_DIR", dir)

	got := findDataDir("")
	if got != dir {
		t.Errorf("findDataDir with env should return env dir, got %q", got)
	}
}

func TestHandleVersion_Output(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleVersion(map[string]interface{}{"source_dir": ""})

	_ = w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	_ = r.Close()

	var resp map[string]interface{}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("failed to parse version response: %v", err)
	}
	if resp["arch"] != "zepto" {
		t.Errorf("expected arch=zepto, got %v", resp["arch"])
	}
	if resp["driver_type"] != "local" {
		t.Errorf("expected driver_type=local, got %v", resp["driver_type"])
	}
}
