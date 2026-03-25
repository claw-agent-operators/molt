// molt-driver-nanoclaw — molt driver for NanoClaw installations.
// Communicates via newline-delimited JSON on stdin/stdout.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const (
	arch          = "nanoclaw"
	driverVersion = "0.1.0"
	moltProtocol  = "0.1.0"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
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
			handleVersion()
		case "probe_request":
			sourceDir, _ := msg["source_dir"].(string)
			handleProbe(sourceDir)
		case "export_request":
			sourceDir, _ := msg["source_dir"].(string)
			handleExport(sourceDir)
		case "import_request":
			destDir, _ := msg["dest_dir"].(string)
			bundleRaw, _ := msg["bundle"]
			handleImport(destDir, bundleRaw)
		default:
			writeError("UNKNOWN_TYPE", fmt.Sprintf("unknown message type: %q", msgType))
		}
	}
}

func handleVersion() {
	write(map[string]interface{}{
		"type":            "version_response",
		"arch":            arch,
		"arch_version":    detectArchVersion(),
		"driver_version":  driverVersion,
		"molt_protocol":   moltProtocol,
		"driver_type":     "local",
		"requires_config": []string{},
	})
}

func handleProbe(sourceDir string) {
	confidence := probeNanoClaw(sourceDir)
	write(map[string]interface{}{
		"type":       "probe_response",
		"arch":       arch,
		"confidence": confidence,
	})
}

func handleExport(sourceDir string) {
	if sourceDir == "" {
		writeError("MISSING_SOURCE", "source_dir is required for nanoclaw driver")
		return
	}
	if err := validateSource(sourceDir); err != nil {
		writeError("SOURCE_NOT_FOUND", err.Error())
		return
	}

	warnings := []string{}

	// 1. Export groups from DB + filesystem
	groups, err := readGroups(sourceDir)
	if err != nil {
		writeError("DB_ERROR", fmt.Sprintf("failed to read groups: %v", err))
		return
	}
	for _, g := range groups {
		write(g)
	}

	// 2. Export scheduled tasks
	tasks, err := readTasks(sourceDir)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("tasks export failed: %v", err))
	} else {
		write(map[string]interface{}{"type": "task_list", "tasks": tasks})
	}

	// 3. Export secret key names (not values)
	keys := readSecretKeys(sourceDir)
	write(map[string]interface{}{"type": "secrets_keys", "keys": keys})

	// 4. Sessions — best effort
	sessionWarnings := exportSessions(sourceDir)
	warnings = append(warnings, sessionWarnings...)

	write(map[string]interface{}{"type": "export_complete", "warnings": warnings})
}

func handleImport(destDir string, bundleRaw interface{}) {
	if destDir == "" {
		writeError("MISSING_DEST", "dest_dir is required for nanoclaw driver")
		return
	}
	// TODO: implement import
	writeError("NOT_IMPLEMENTED", "nanoclaw import not yet implemented")
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
