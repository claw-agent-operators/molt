// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ── shellCompletionPath ───────────────────────────────────────────────────────

func TestShellCompletionPath_DefaultXDG(t *testing.T) {
	// Unset XDG_DATA_HOME so the fallback (~/.local/share) is used.
	t.Setenv("XDG_DATA_HOME", "")

	home, _ := os.UserHomeDir()

	cases := []struct {
		shell string
		want  string
	}{
		{"bash", filepath.Join(home, ".local", "share", "bash-completion", "completions", "molt")},
		{"zsh", filepath.Join(home, ".zsh", "completions", "_molt")},
		{"fish", filepath.Join(home, ".local", "share", "fish", "vendor_completions.d", "molt.fish")},
	}
	for _, tc := range cases {
		got, err := shellCompletionPath(tc.shell)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.shell, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.shell, got, tc.want)
		}
	}
}

func TestShellCompletionPath_CustomXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/xdg")

	got, err := shellCompletionPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	want := "/custom/xdg/bash-completion/completions/molt"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	got, err = shellCompletionPath("fish")
	if err != nil {
		t.Fatal(err)
	}
	want = "/custom/xdg/fish/vendor_completions.d/molt.fish"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShellCompletionPath_UnknownShell(t *testing.T) {
	_, err := shellCompletionPath("powershell")
	if err == nil {
		t.Error("expected error for unknown shell, got nil")
	}
}

// ── runCompletion output ──────────────────────────────────────────────────────

func TestRunCompletion_OutputNonEmpty(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var buf bytes.Buffer
		completionInstall = false

		// Redirect stdout by temporarily replacing os.Stdout.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("%s: pipe: %v", shell, err)
		}
		old := os.Stdout
		os.Stdout = w

		runErr := runCompletion(completionCmd, []string{shell})

		_ = w.Close()
		os.Stdout = old
		_, _ = buf.ReadFrom(r)

		if runErr != nil {
			t.Errorf("%s: runCompletion returned error: %v", shell, runErr)
		}
		if buf.Len() == 0 {
			t.Errorf("%s: completion output was empty", shell)
		}
		out := buf.String()
		if !strings.Contains(out, "molt") {
			t.Errorf("%s: completion output does not mention 'molt':\n%s", shell, out[:min(200, len(out))])
		}
	}
}

func TestRunCompletion_ShellHeaders(t *testing.T) {
	// Each shell's completion script starts with a recognisable header.
	// Cobra bash completion V2 resolves subcommands dynamically at completion
	// time (via `molt __complete`), so subcommand names are not literals in
	// the generated script.
	cases := []struct {
		shell  string
		header string
	}{
		{"bash", "# bash completion V2 for molt"},
		{"zsh", "#compdef molt"},
		{"fish", "# fish completion for molt"},
	}
	for _, tc := range cases {
		r, w, _ := os.Pipe()
		old := os.Stdout
		os.Stdout = w
		completionInstall = false

		_ = runCompletion(completionCmd, []string{tc.shell})

		_ = w.Close()
		os.Stdout = old
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		out := buf.String()

		if !strings.Contains(out, tc.header) {
			t.Errorf("%s: expected header %q in output", tc.shell, tc.header)
		}
	}
}

// ── installCompletion ─────────────────────────────────────────────────────────

func TestInstallCompletion_WritesFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	for _, shell := range []string{"bash", "fish"} {
		if err := installCompletion(shell); err != nil {
			t.Fatalf("%s: installCompletion error: %v", shell, err)
		}
		path, _ := shellCompletionPath(shell)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s: file not created at %s: %v", shell, path, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s: completion file is empty", shell)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "molt") {
			t.Errorf("%s: completion file does not mention 'molt'", shell)
		}
	}
}

func TestInstallCompletion_Zsh(t *testing.T) {
	home := t.TempDir()
	// Override UserHomeDir indirectly via HOME env var.
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "") // ensure XDG fallback uses the temp home

	if err := installCompletion("zsh"); err != nil {
		t.Fatalf("installCompletion zsh error: %v", err)
	}
	path := filepath.Join(home, ".zsh", "completions", "_molt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("zsh completion file not created: %v", err)
	}
	if !strings.Contains(string(data), "molt") {
		t.Error("zsh completion file does not mention 'molt'")
	}
}

// ── completeArchs ─────────────────────────────────────────────────────────────

func TestCompleteArchs_StaticFallback(t *testing.T) {
	// With no drivers installed (empty PATH / no ~/.molt/drivers), completeArchs
	// must return the static fallback list, not an empty slice.
	t.Setenv("PATH", t.TempDir()) // empty dir — no drivers
	t.Setenv("HOME", t.TempDir()) // no ~/.molt/drivers/

	archs, directive := completeArchs(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
	if len(archs) == 0 {
		t.Fatal("expected fallback arch list, got empty slice")
	}
	want := map[string]bool{"nanoclaw": true, "zepto": true, "openclaw": true, "pico": true}
	for _, a := range archs {
		if !want[a] {
			t.Errorf("unexpected arch in fallback list: %q", a)
		}
	}
}

// ── completeMoltFile / completeMoltFileOrDir ──────────────────────────────────

func TestCompleteMoltFile(t *testing.T) {
	exts, dir := completeMoltFile(nil, nil, "")
	if dir != cobra.ShellCompDirectiveFilterFileExt {
		t.Errorf("arg 0: directive = %v, want FilterFileExt", dir)
	}
	if len(exts) != 1 || exts[0] != "molt" {
		t.Errorf("arg 0: exts = %v, want [molt]", exts)
	}

	exts, dir = completeMoltFile(nil, []string{"bundle.molt"}, "")
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("arg 1: directive = %v, want NoFileComp", dir)
	}
	if exts != nil {
		t.Errorf("arg 1: expected nil completions, got %v", exts)
	}
}

func TestCompleteMoltFileOrDir(t *testing.T) {
	cases := []struct {
		args     []string
		wantExts []string
		wantDir  cobra.ShellCompDirective
	}{
		{nil, []string{"molt"}, cobra.ShellCompDirectiveFilterFileExt},
		{[]string{"bundle.molt"}, nil, cobra.ShellCompDirectiveFilterDirs},
		{[]string{"bundle.molt", "/dest"}, nil, cobra.ShellCompDirectiveNoFileComp},
	}
	for _, tc := range cases {
		exts, dir := completeMoltFileOrDir(nil, tc.args, "")
		if dir != tc.wantDir {
			t.Errorf("args=%v: directive = %v, want %v", tc.args, dir, tc.wantDir)
		}
		if len(exts) != len(tc.wantExts) {
			t.Errorf("args=%v: exts = %v, want %v", tc.args, exts, tc.wantExts)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
