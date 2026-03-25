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
{"type": "skill", "name": "downwind", "files": [...]}
{"type": "session", "slug": "main", "best_effort": true, "data": {...}}
{"type": "secrets_keys", "keys": ["ANTHROPIC_API_KEY", "SIGNAL_ACCOUNT"]}
{"type": "export_complete", "warnings": []}
```

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
