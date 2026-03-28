// SPDX-License-Identifier: AGPL-3.0-or-later
// molt-driver-zepto — molt driver for ZeptoClaw installations.
// Communicates via newline-delimited JSON on stdin/stdout.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const (
	arch          = "zepto"
	driverVersion = "0.1.0"
	moltProtocol  = "0.1.0"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 200*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			writeError("PARSE_ERROR", fmt.Sprintf("invalid JSON: %v", err))
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "version_request":
			handleVersion(msg)
		case "probe_request":
			sourceDir, _ := msg["source_dir"].(string)
			handleProbe(sourceDir)
		case "export_request":
			sourceDir, _ := msg["source_dir"].(string)
			handleExport(sourceDir)
		case "import_request":
			destDir, _ := msg["dest_dir"].(string)
			bundleRaw := msg["bundle"]
			renamesRaw, _ := msg["renames"].(map[string]interface{})
			renames := make(map[string]string, len(renamesRaw))
			for k, v := range renamesRaw {
				if s, ok := v.(string); ok {
					renames[k] = s
				}
			}
			handleImport(destDir, bundleRaw, renames)
		default:
			writeError("UNKNOWN_TYPE", fmt.Sprintf("unknown message type: %q", msgType))
		}
	}
}

func handleVersion(req map[string]interface{}) {
	sourceDir, _ := req["source_dir"].(string)
	write(map[string]interface{}{
		"type":            "version_response",
		"arch":            arch,
		"arch_version":    detectArchVersion(sourceDir),
		"driver_version":  driverVersion,
		"molt_protocol":   moltProtocol,
		"driver_type":     "local",
		"requires_config": []string{},
	})
}

func handleProbe(sourceDir string) {
	confidence := probeZeptoClaw(sourceDir)
	write(map[string]interface{}{
		"type":       "probe_response",
		"arch":       arch,
		"confidence": confidence,
	})
}

func handleExport(sourceDir string) {
	if sourceDir == "" {
		writeError("MISSING_SOURCE", "source_dir is required for zepto driver")
		return
	}
	if err := validateSource(sourceDir); err != nil {
		writeError("SOURCE_NOT_FOUND", err.Error())
		return
	}

	warnings := []string{}

	// 1. Export groups from channels.json + filesystem
	groups, groupWarnings := readGroups(sourceDir)
	warnings = append(warnings, groupWarnings...)
	for _, g := range groups {
		write(g)
	}

	// 2. Export scheduled tasks from cron/
	tasks, taskWarnings := readTasks(sourceDir)
	warnings = append(warnings, taskWarnings...)
	write(map[string]interface{}{"type": "task_list", "tasks": tasks})

	// 3. Export secret key names from config.json
	keys := readSecretKeys(sourceDir)
	write(map[string]interface{}{"type": "secrets_keys", "keys": keys})

	// 4. Sessions — best effort
	sessionWarnings := exportSessions(sourceDir)
	warnings = append(warnings, sessionWarnings...)

	// 5. Skills — not applicable for ZeptoClaw
	warnings = append(warnings, "skills export not supported for ZeptoClaw (different skill system)")

	write(map[string]interface{}{
		"type":            "export_complete",
		"warnings":        warnings,
		"skills_exported": 0,
	})
}

func handleImport(destDir string, bundleRaw interface{}, renames map[string]string) {
	if destDir == "" {
		writeError("MISSING_DEST", "dest_dir is required for zepto driver")
		return
	}
	doImport(destDir, bundleRaw, renames)
}

// write emits one ndjson line to stdout.
func write(v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}

// writeError emits an error message.
func writeError(code, message string) {
	write(map[string]string{"type": "error", "code": code, "message": message})
}
