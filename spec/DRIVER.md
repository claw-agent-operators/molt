# Molt Driver Interface

Version: 0.1.0

A molt driver is a standalone executable that knows how to read from and write to a specific claw architecture. `molt` locates drivers by looking for `molt-driver-<arch>` in `$PATH` or `~/.molt/drivers/`.

## Naming convention

```
molt-driver-nanoclaw          # local: NanoClaw
molt-driver-zepto             # local: ZeptoClaw
molt-driver-openclaw          # local: OpenClaw
molt-driver-pico              # local: PicoClaw
molt-driver-anthropic-claw    # remote: Anthropic Claw (SaaS)
molt-driver-cloudflare-claw   # remote: Cloudflare Workers AI Claw
molt-driver-nvidia-claw       # remote: NVIDIA AI Workbench Claw
```

## Driver types

**Local drivers** read from and write to a local filesystem installation.
`source_dir` / `dest_dir` are required.

**Remote drivers** interact with a SaaS API. `source_dir` / `dest_dir` are
empty string `""`. Auth and endpoint are passed via `config`.

## Protocol

Drivers communicate with `molt` via stdin/stdout using newline-delimited JSON.
All messages have a `type` field.

### Version check

```json
{"type": "version_request"}
```

```json
{
  "type": "version_response",
  "arch": "nanoclaw",
  "arch_version": "1.4.2",
  "driver_version": "0.1.0",
  "molt_protocol": "0.1.0",
  "driver_type": "local",
  "requires_config": []
}
```

For remote drivers:

```json
{
  "type": "version_response",
  "arch": "anthropic-claw",
  "arch_version": "1.0",
  "driver_version": "0.1.0",
  "molt_protocol": "0.1.0",
  "driver_type": "remote",
  "requires_config": ["api_url", "api_key"]
}
```

`requires_config` lists the keys the driver needs in `export_request.config`
or `import_request.config`. `molt archs` shows these so the user knows what
to provide.

### Export (molt → driver)

Local:
```json
{"type": "export_request", "source_dir": "/path/to/install", "config": {}}
```

Remote:
```json
{
  "type": "export_request",
  "source_dir": "",
  "config": {
    "api_url": "https://claw.anthropic.com",
    "api_key": "sk-..."
  }
}
```

Driver responds with a stream of bundle parts:

```json
{"type": "group", "slug": "main", "config": {...}, "files": [...]}
{"type": "task_list", "tasks": [...]}
{"type": "secrets_keys", "keys": ["ANTHROPIC_API_KEY", "SIGNAL_ACCOUNT"]}
{"type": "skill_manifest", "skills": {"downwind": ["main", "surf-crew"], "clawsec-suite": ["main"]}}
{"type": "skill", "name": "downwind", "files": [{"path": "skill.json", "content": "<base64>"}, ...]}
{"type": "session", "slug": "main", "best_effort": true, "files": [...]}
{"type": "export_complete", "warnings": [], "skills_exported": 2}
```

### `skill_manifest` message

Emitted once before any `skill` messages when user-installed skills are present. Maps each skill name to the group slugs that had it installed. Import drivers use this to restore skills only to the correct groups.

```json
{
  "type": "skill_manifest",
  "skills": {
    "downwind": ["main", "surf-crew"],
    "clawsec-suite": ["main"]
  }
}
```

Drivers SHOULD omit `skill_manifest` (and all `skill` messages) if no user-installed skills are found.

### `skill` message

One message per unique user-installed skill. Files are base64-encoded (same encoding as `group` files).

```json
{
  "type": "skill",
  "name": "downwind",
  "files": [
    {"path": "skill.json",  "content": "<base64>"},
    {"path": "SKILL.md",    "content": "<base64>"},
    {"path": "_meta.json",  "content": "<base64>"}
  ]
}
```

A skill is considered user-installed if its directory contains `_meta.json`. Drivers MUST skip skills without `_meta.json` — these are container/built-in skills that ship with the target architecture.

### `export_complete` fields

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | `"export_complete"` |
| `warnings` | []string | Non-fatal warnings from export |
| `skills_exported` | int | Count of distinct user-installed skills exported (omit or 0 if none) |

### Import (molt → driver)

Local:
```json
{"type": "import_request", "dest_dir": "/path/to/new/install", "config": {}, "bundle": {...}}
```

Remote:
```json
{
  "type": "import_request",
  "dest_dir": "",
  "config": {"api_url": "...", "api_key": "..."},
  "bundle": {...}
}
```

Driver responds:

```json
{"type": "progress", "message": "Registering group: main"}
{"type": "collision", "slug": "main"}
{"type": "progress", "message": "Importing tasks"}
{"type": "import_complete", "warnings": ["Session import best-effort: main"]}
```

## Import atomicity

Import SHOULD be all-or-nothing for groups and DB state. If any group's DB insert fails, the driver MUST roll back the entire import (including any filesystem paths created so far) and emit an `error` message rather than leaving a partial state.

Sessions are exempt: session import is always best-effort and happens after the main transaction commits. A session import failure MUST be reported as a warning, not an error.

Filesystem cleanup is best-effort — it is not guaranteed if the driver process is killed mid-import.

## Two-pass import for symlinked groups

When a bundle contains groups with `_arch_nanoclaw.symlink_target`, drivers MUST use a two-pass approach:

- **Pass 1**: process real (non-symlink) groups — write files, insert DB rows
- **Pass 2**: process symlink groups — after pass 1, all targets should be on disk

Before creating a symlink in pass 2, drivers MUST validate:
1. The target slug exists on disk as a real directory
2. The target is NOT itself a symlink (catches circular chains: A→B, B→A)

If validation fails, the symlink MUST be skipped with a warning — a missing symlink is recoverable; a corrupt or circular symlink is not.

## Collision handling

When a driver emits `{"type": "collision", "slug": "..."}`, molt pauses,
resolves via `--rename` flag or aborts with a ready-to-run fix:

```
Error: agent slug collision — "main" already exists in dest.
Re-run with:
  molt import bundle.molt /dest --arch nanoclaw --rename main=main-imported
```

The driver waits for either:

```json
{"type": "collision_resolved", "original": "main", "renamed_to": "main-imported"}
```

or

```json
{"type": "abort"}
```

## Error handling

```json
{"type": "error", "code": "SOURCE_NOT_FOUND", "message": "No NanoClaw installation found at /path"}
```

Fatal errors terminate the stream. Non-fatal errors are collected as warnings
in the final `export_complete` / `import_complete` message.

## Sessions

Session export/import is always best-effort. Drivers MUST emit `"best_effort": true`
on session messages. `molt` surfaces a warning:

```
⚠ Sessions exported best-effort — Claude session IDs may not be valid in target arch.
```

## Auto-detection (local drivers only)

When `--arch` is not specified for export, `molt` probes installed local drivers:

```json
{"type": "probe_request", "source_dir": "/path/to/install"}
```

```json
{"type": "probe_response", "confidence": 0.95, "arch": "nanoclaw"}
```

Remote drivers MUST return `confidence: 0.0` for probe requests — they cannot
auto-detect from a local path.

## Remote driver config

Auth and connection details are passed per-request in `config`. For persistent
credentials, users store them in `~/.molt/configs/<arch>.json`:

```json
{
  "api_url": "https://claw.anthropic.com",
  "api_key": "sk-..."
}
```

`molt` reads this file and merges it into every request to that driver.
`--config key=value` flags override the file at runtime.

## `molt archs` output

```
ARCH                 TYPE    ARCH VER     DRIVER VER   LOCATION
nanoclaw             local   1.4.2        0.1.0        /usr/local/bin/molt-driver-nanoclaw
anthropic-claw       remote  1.0          0.1.0        /usr/local/bin/molt-driver-anthropic-claw
                             requires: api_url, api_key
```

## Minimal viable driver

A driver MUST implement:
- `version_request` / `version_response` (with `driver_type`)
- `export_request` with at least `group` messages and `secrets_keys`
- `import_request` with group registration

Sessions, tasks, skills, and `probe_request` are optional (emit warnings if skipped).
