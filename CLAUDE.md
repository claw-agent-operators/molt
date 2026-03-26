# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and test

```bash
# Build molt binary → ./build/molt
make build

# Build all drivers → ./build/molt-driver-*
make build-drivers

# Build and install everything → ~/.local/bin/
make install-all

# Run all tests (molt + all drivers)
make test

# Run tests for molt only
go test ./...

# Run tests for the nanoclaw driver only
cd drivers/nanoclaw && go test ./...

# Run a single test
cd drivers/nanoclaw && go test -run TestRoundTrip ./...

# Lint (requires golangci-lint)
make lint
```

The nanoclaw CLI tests (`cli_test.go`) auto-build the molt and driver binaries in `TestMain` and skip themselves if the build fails — no manual setup required.

## Architecture

**Two separate Go modules:**
- Root module (`go.mod`) — the `molt` CLI binary, in `src/`
- `drivers/nanoclaw/go.mod` — the nanoclaw driver, a standalone binary

Each driver is independently built and located at runtime via `$PATH` or `~/.molt/drivers/`. Adding a new driver means creating a `drivers/<arch>/` directory with its own `go.mod`.

**Data flow for export:**
```
molt export → driver.Export() → spawns molt-driver-<arch>
                              → streams NDJSON to bundle.Assembler.Feed()
                              → assembler builds Bundle{Manifest, Files map}
                              → Bundle.SaveTo() writes gzipped tar
```

**Data flow for import:**
```
molt import → bundle.Load() → driver.Import()
                            → sends full bundle JSON to driver stdin
                            → driver.doImport() writes files + DB in a transaction
                            → sessions/skills imported post-commit, best-effort
```

**Key packages:**
- `src/bundle/` — `Bundle` struct (in-memory .molt file), `Assembler` (NDJSON → Bundle), tar read/write
- `src/driver/` — driver discovery, version probe, `Export()`/`Import()` protocol wrappers
- `src/cmd/` — Cobra CLI commands (`export`, `import`, `inspect`, `upgrade`, `archs`, `completion`)
- `drivers/nanoclaw/` — standalone NanoClaw driver binary

## Driver protocol

Drivers communicate via newline-delimited JSON on stdin/stdout. Export stream message order:
1. `group` (one per group) — config + base64-encoded files
2. `task_list` — all scheduled tasks
3. `secrets_keys` — key names only (values never exported)
4. `skill_manifest` — maps skill names → group slugs (omitted if no user-installed skills)
5. `skill` (one per unique skill) — base64-encoded files
6. `session` (one per group, best-effort) — base64-encoded files
7. `export_complete` — warnings + `skills_exported` count

The assembler in `src/bundle/assemble.go` handles all message types. Unknown types are silently ignored (forward-compat).

## Bundle format

A `.molt` file is a gzipped tar. All file content is base64-encoded (Go's `encoding/base64` standard encoding). The `manifest.json` inside includes `groups []string` and `skills map[string][]string` (skill → group slugs). See `spec/BUNDLE.md` for the full layout.

## NanoClaw driver internals

- `groups.go` — reads groups from `store/messages.db` + walks `groups/<slug>/` directories
- `sessions.go` — best-effort walk of `data/sessions/<slug>/`
- `skills.go` — discovers user-installed skills in `data/sessions/<slug>/.claude/skills/`; gated on `_meta.json` presence (built-ins have no `_meta.json`)
- `import.go` — two-pass import: real groups first, symlinked groups second; DB + filesystem in one transaction; sessions and skills post-commit best-effort
- `db.go` — SQLite helpers and arch version detection from `package.json`

Import atomicity: groups and DB inserts are wrapped in a single transaction with filesystem cleanup on failure. Sessions and skills are post-commit and failures are warnings, not errors.

## Spec

`spec/BUNDLE.md` and `spec/DRIVER.md` are the authoritative specs. Keep them in sync when adding new message types or bundle fields.
