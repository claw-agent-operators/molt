# molt-driver-nanoclaw

The molt driver for [NanoClaw](https://github.com/qwibitai/nanoclaw) installations.

## Installation

```bash
# Build and install via the molt repo root (installs to ~/.local/bin/)
make install-drivers

# Or build the driver directly
cd drivers/nanoclaw && go build -o molt-driver-nanoclaw .
cp molt-driver-nanoclaw ~/.local/bin/
```

molt locates drivers via `~/.molt/drivers/` or `$PATH`. The binary must be named `molt-driver-nanoclaw`.

## What gets exported

| Source | Contents | Notes |
|--------|----------|-------|
| `store/messages.db` | Group registrations (name, JID, trigger, config) | All registered groups |
| `store/messages.db` | Scheduled tasks | Full task config |
| `groups/<slug>/` | All files in group directories | See limits below |
| `groups/global/` | Global CLAUDE.md and memory | Included as unregistered group |
| `data/sessions/<slug>/` | Claude session cache | Best-effort only |
| `data/sessions/<slug>/.claude/skills/<name>/` | User-installed skills | Requires `_meta.json`; built-ins skipped |
| `.env` | Secret key names | Values never exported |

## What gets skipped

**Directories** — always excluded from group file walks:
- `logs/` — runtime logs, not portable
- `.git/` — version control metadata
- `agent-runner-src/` — build artifacts

**Large files:**
- Group files over 10 MB — skipped with a warning in export output
- Session files over 5 MB — skipped with a warning

## Symlinked groups

NanoClaw supports symlinking a group directory to another (e.g. `main-signal → main`). The driver exports symlinks as references rather than duplicating files. On import, symlinks are created in a second pass after all real group directories exist. A symlink whose target is missing or is itself a symlink is skipped with a warning.

## Sessions

Session export is best-effort. Session IDs (UUIDs) in the Claude session cache are tied to the source installation and may not be valid in the target. Sessions are imported to the same relative path and can be used as a starting point, but Claude may treat them as stale.

## Known limitations

- **Container images** — not included; must be rebuilt in the target environment
- **Session ID validity** — session IDs may not be recognized by the target installation
- **Absolute paths in config** — any absolute paths embedded in group config files are not rewritten on import
