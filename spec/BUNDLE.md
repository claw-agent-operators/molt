# Molt Bundle Format

Version: 0.1.0

A `.molt` file is a gzipped tar archive with a predictable directory layout. Every field is UTF-8. The format is designed to be human-readable, diff-friendly, and forward-compatible.

## Layout

```
bundle.molt (gzipped tar)
├── manifest.json
├── secrets-template.env
├── groups/
│   ├── <slug>/
│   │   ├── config.json          # group registration
│   │   ├── CLAUDE.md            # per-group memory/instructions
│   │   ├── conversations/       # conversation history (optional)
│   │   │   └── <id>.json
│   │   └── files/               # arbitrary group files
│   │       └── ...
│   └── global/
│       └── CLAUDE.md            # global memory
├── tasks.json                   # scheduled tasks
├── skills/                      # user-installed skills
│   └── <skill-name>/
│       ├── _meta.json           # version + ownerId (user-installed signal)
│       ├── skill.json           # or manifest.yaml — verbatim
│       ├── SKILL.md             # if present
│       └── ...                  # other skill files verbatim
└── sessions/                    # best-effort session cache
    └── <slug>/
        └── ...
```

## manifest.json

```json
{
  "molt_version": "0.1.0",
  "created_at": "2026-03-25T12:00:00Z",
  "source": {
    "arch": "nanoclaw",
    "arch_version": "1.4.2",
    "hostname": "optional-or-redacted"
  },
  "imported_to": {
    "arch": "zepto",
    "arch_version": "0.9.1",
    "imported_at": "2026-03-26T09:00:00Z"
  },
  "groups": ["main", "kycsitescan", "tanglewylde"],
  "skills": {
    "downwind": ["main", "surf-crew"],
    "clawsec-suite": ["main"]
  },
  "warnings": []
}
```

`imported_to` is added by the driver on import. A bundle that has never been imported has `imported_to: null`.

`skills` maps each skill name to the list of group slugs that had it installed. Absent when no user-installed skills were found. Used by the import driver to restore skills only to the groups that had them.

`checksums` is reserved for future use. Implementations MUST ignore this field in 0.1.x bundles.

## groups/<slug>/config.json

Normalized group registration. Arch-specific fields go in `_arch_<name>`.

```json
{
  "slug": "main",
  "name": "Main",
  "jid": "signal:group:ypvV...",
  "trigger": "@Andy",
  "agent_name": null,
  "requires_trigger": false,
  "is_main": true,
  "added_at": "2026-01-15T08:00:00Z",
  "_arch_nanoclaw": {
    "container_config": {
      "additionalMounts": []
    },
    "is_default_dm": false
  }
}
```

## conversations/<id>.json

Common schema. Arch-specific fields preserved under `_arch_<name>`.

```json
{
  "id": "conv-2026-03-24-0842",
  "group_slug": "main",
  "started_at": "2026-03-24T08:42:00Z",
  "ended_at": "2026-03-24T09:15:00Z",
  "messages": [
    {
      "role": "user",
      "content": "what is on deck?",
      "timestamp": "2026-03-24T13:55:00Z",
      "sender": "+19175126534"
    },
    {
      "role": "assistant",
      "content": "Three things left...",
      "timestamp": "2026-03-24T13:55:10Z"
    }
  ],
  "_arch_nanoclaw": {
    "reactions": [],
    "session_id": "0ca01815-7a19-4260-9e18-df218f8ed0b0"
  }
}
```

## tasks.json

```json
[
  {
    "id": "task-1774277047023-jfxkiy",
    "group_slug": "main",
    "prompt": "Check OluKai Moloa and L.L. Bean Wicked Good for sales...",
    "schedule_type": "cron",
    "schedule_value": "0 8 * * 2",
    "context_mode": "isolated",
    "created_at": "2026-03-23T10:43:00Z",
    "active": true
  }
]
```

## secrets-template.env

Key names only. Values are never included.

```bash
# molt secrets template
# Fill in before starting <target-arch>
# Exported from nanoclaw @ 2026-03-25T09:00:00Z

ANTHROPIC_API_KEY=
CLAUDE_CODE_OAUTH_TOKEN=
SIGNAL_ACCOUNT=
GITHUB_TOKEN=
```

## What moves

| Data | Migrated | Notes |
|------|----------|-------|
| Groups | ✓ | Config + files |
| Tasks | ✓ | Scheduled tasks |
| Secrets | template only | Key names only; values never included |
| Skills | ✓ (user-installed only) | Container/built-in skills excluded; see `_meta.json` note below |
| Sessions | ✓ (best-effort) | Session IDs may not be valid in target arch |

### Skills: user-installed vs. built-in

A skill directory is considered user-installed if it contains `_meta.json`. Drivers SHOULD skip skills without `_meta.json` — these ship with the target architecture and will be present after install. Migrating them would overwrite potentially newer versions.

On import, if a skill already exists in the destination:
- Same version: skipped silently
- Different version: skipped with a warning (dest may be newer)
- Unknown state (no `_meta.json` in dest): skipped with a warning

If the bundle did not include `_meta.json` for a skill, the import driver synthesizes a minimal one:
```json
{"slug": "<name>", "version": "unknown", "migratedBy": "molt"}
```

## File content encoding

All file content in bundle messages (`group`, `session`, `skill`) is base64-encoded (`encoding/base64` standard encoding). This applies to both the wire format (driver → assembler) and the in-bundle representation (`Files` map). Decoders MUST reject invalid base64 — falling back to treating content as raw text risks binary file corruption.

## Limits

Per-driver file size caps. Files exceeding these limits are skipped with a warning in `export_complete.warnings`; they are not included in the bundle.

| Driver | Group files | Session files | Skill files |
|--------|------------|---------------|-------------|
| nanoclaw | 10 MB | 5 MB | no limit (skills are small) |

Other drivers MAY define their own limits. Consumers should treat missing files as expected when warnings are present.

## Versioning

- `molt_version` in manifest tracks the bundle format version (semver)
- On import, if bundle version < current molt version, warn and proceed (best-effort)
- Use `molt upgrade <bundle>` to explicitly rewrite to current format
- Unknown fields are preserved verbatim — never silently dropped

## Future: Registry (v2 target)

Driver discovery via an optional ClawHub-style registry. Core tool remains peer-to-peer; registry is opt-in via `molt registry add <url>`.
