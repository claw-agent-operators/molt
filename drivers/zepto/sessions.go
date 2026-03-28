// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxSessionFileSize is the max size for a session file to be included in export.
const MaxSessionFileSize = 5 * 1024 * 1024 // 5 MB

// exportSessions emits session messages from the ZeptoClaw sessions/ directory.
// ZeptoClaw stores sessions as individual JSON files: sessions/<key>.json
// Each file has: {key, updated_at, messages: [{role, content}]}
func exportSessions(sourceDir string) []string {
	dataDir := findDataDir(sourceDir)
	sessDir := filepath.Join(dataDir, "sessions")
	var warnings []string

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		// sessions/ may not exist — not an error
		return nil
	}

	// Group session files by group slug extracted from session key.
	// Session keys have format "channel:chat_id" — we use the full key as filename.
	// For export, we just bundle all sessions together since ZeptoClaw doesn't
	// have per-group session directories.
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > MaxSessionFileSize {
			warnings = append(warnings, fmt.Sprintf(
				"sessions/%s: skipped (%.1f MB > 5 MB limit)",
				e.Name(), float64(info.Size())/(1024*1024)))
			continue
		}

		content, err := os.ReadFile(filepath.Join(sessDir, e.Name()))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("sessions/%s: read error: %v", e.Name(), err))
			continue
		}

		// Extract slug from the session key if possible
		slug := sessionSlug(content, e.Name())

		write(map[string]interface{}{
			"type":        "session",
			"slug":        slug,
			"best_effort": true,
			"files": []BundleFile{{
				Path:    e.Name(),
				Content: base64.StdEncoding.EncodeToString(content),
			}},
		})
	}

	return warnings
}

// sessionSlug extracts a group slug from a session file.
// Tries to parse the session key; falls back to the filename.
func sessionSlug(data []byte, filename string) string {
	var raw struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(data, &raw) == nil && raw.Key != "" {
		// Session key format: "channel:chat_id" — use as-is since
		// there's no direct mapping to a group slug in ZeptoClaw.
		// Fall back to "zepto-sessions" as a catch-all slug.
		return "zepto-sessions"
	}
	return "zepto-sessions"
}

// importSessions writes session files to the destination sessions/ directory.
// This is best-effort: session format may differ between architectures.
func importSessions(destDir string, files map[string]string, renames map[string]string, warnings *[]string) {
	dataDir := findDataDir(destDir)
	sessDir := filepath.Join(dataDir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("failed to create sessions dir: %v", err))
		return
	}

	for bundlePath, b64Content := range files {
		if !strings.HasPrefix(bundlePath, "sessions/") {
			continue
		}

		content, err := base64.StdEncoding.DecodeString(b64Content)
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("%s: invalid base64", bundlePath))
			continue
		}

		// Convert nanoclaw JSONL sessions to zepto JSON format (best-effort)
		converted := convertSessionToZepto(content, bundlePath)

		// Write to sessions/ directory, flattening the path
		filename := filepath.Base(bundlePath)
		destPath := filepath.Join(sessDir, filename)
		if err := os.WriteFile(destPath, converted, 0o644); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("%s: write error: %v", bundlePath, err))
		}
	}
}

// convertSessionToZepto converts a session file to ZeptoClaw JSON format.
// If the input is already valid ZeptoClaw JSON, it's returned as-is.
// If it looks like nanoclaw JSONL, we extract user/assistant messages.
func convertSessionToZepto(data []byte, path string) []byte {
	// Try parsing as JSON first (might already be zepto format)
	var zeptoSession struct {
		Key      string `json:"key"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(data, &zeptoSession) == nil && len(zeptoSession.Messages) > 0 {
		return data // already in zepto format
	}

	// Try parsing as JSONL (nanoclaw format — one JSON object per line)
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var messages []message

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]interface{}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		// Look for message events with user/assistant role
		role, _ := event["role"].(string)
		if role != "user" && role != "assistant" {
			continue
		}
		// Try to extract text content
		text := extractTextContent(event)
		if text != "" {
			messages = append(messages, message{Role: role, Content: text})
		}
	}

	if len(messages) == 0 {
		return data // couldn't convert, return as-is
	}

	result := struct {
		Key      string    `json:"key"`
		Messages []message `json:"messages"`
	}{
		Key:      strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Messages: messages,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return out
}

// extractTextContent tries to get a text string from a message event's content field.
func extractTextContent(event map[string]interface{}) string {
	// Direct string content
	if s, ok := event["content"].(string); ok {
		return s
	}
	// Array of content blocks: [{type: "text", text: "..."}]
	if arr, ok := event["content"].([]interface{}); ok {
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, ok := m["text"].(string); ok {
						return text
					}
				}
			}
		}
	}
	return ""
}
