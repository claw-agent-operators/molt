# molt sync — Spec

`molt sync` is continuous, scheduled, incremental backup for claw agent
installations. Where `molt export` is a one-shot operation, `molt sync`
runs as a daemon — watching for changes, exporting deltas, and maintaining
a recoverable snapshot store at a configurable destination.

It is the disaster recovery story for production claw deployments.

---

## Commands

```
molt sync start                          Start the sync daemon for the default installation
  --source <dir>                         Source installation directory (default: auto-detect)
  --arch <name>                          Source architecture (default: auto-detect)
  --dest <path>                          Destination: local dir, s3://, sftp://, rsync:// URI
  --interval <duration>                  How often to sync: 1h, 6h, 24h (default: 6h)
  --full-every <N>                       Full export every N syncs; others are incremental (default: 7)
  --retain <N>                           Keep N snapshots at dest; prune older ones (default: 14)
  --on-change                            Also sync immediately when files change (fsnotify)
  --daemonize                            Fork to background and write PID to ~/.molt/sync.pid

molt sync stop                           Stop the running daemon
molt sync status                         Show daemon state, last/next run, dest health
molt sync now                            Run a sync immediately (daemon must be running)
molt sync restore <snapshot>             Restore a snapshot to the source installation
  --dry-run                              Show what would be restored, make no changes
  --group <slug>                         Restore only one group
molt sync list                           List available snapshots at the configured dest
molt sync log                            Tail the sync log (follows if daemon is running)
```

---

## Destination URIs

| Scheme | Example | Notes |
|--------|---------|-------|
| Local dir | `/Volumes/Backup/nanoclaw` | Fastest; no auth needed |
| S3 | `s3://my-bucket/nanoclaw-backup` | Requires AWS credentials in env or `~/.aws` |
| SFTP | `sftp://user@host/path/to/backup` | SSH key auth; host must be in known_hosts |
| rsync | `rsync://user@host::module/path` | rsync daemon protocol |
| rsync+ssh | `rsync+ssh://user@host/path` | rsync over SSH |

Credentials are never stored in the sync config — they come from the environment
or standard credential stores (AWS, SSH agent).

---

## Snapshot layout at destination

Each sync writes a dated snapshot alongside a `latest` symlink:

```
<dest>/
├── latest -> 2026-03-27T06:00:00Z/
├── 2026-03-27T06:00:00Z/
│   ├── manifest.json         # snapshot metadata
│   └── bundle.molt           # full or incremental molt bundle
├── 2026-03-26T06:00:00Z/
│   ├── manifest.json
│   └── bundle.molt
└── ...
```

### Snapshot manifest.json

```json
{
  "created_at": "2026-03-27T06:00:00Z",
  "source_dir": "/Users/you/src/nanoclaw",
  "arch": "nanoclaw",
  "arch_version": "1.4.2",
  "molt_version": "0.2.0",
  "type": "incremental",
  "base_snapshot": "2026-03-26T06:00:00Z",
  "groups": ["main", "dev", "family"],
  "size_bytes": 142832,
  "checksum": "sha256:abc123..."
}
```

`type` is `"full"` or `"incremental"`. Incremental snapshots record their
`base_snapshot` so `molt sync restore` can reconstruct the full state by
replaying the chain.

---

## Incremental export

Incremental syncs export only what changed since the last full or incremental
snapshot. The driver compares:

- Group `CLAUDE.md` and files — mtime-based change detection
- Conversations — new files only (append-only by convention)
- Scheduled tasks — full list (small, always included)
- Skills — compare installed set against last snapshot manifest
- Sessions — skipped in incremental; sessions are large and volatile

The delta bundle is a valid `.molt` file with a `partial: true` flag in its
manifest. `molt sync restore` knows to merge it onto a base.

Every Nth sync (`--full-every`, default 7) exports a complete snapshot
regardless of changes. This bounds the restore chain length and provides
a clean recovery point.

---

## Restore

`molt sync restore` reconstructs a point-in-time state from the snapshot store.

```
molt sync restore latest                         # most recent snapshot
molt sync restore 2026-03-26T06:00:00Z           # specific timestamp
molt sync restore latest --group dev             # restore one group only
molt sync restore 2026-03-26T06:00:00Z --dry-run # preview
```

Restore process:
1. Locate the named snapshot
2. If incremental, walk back to the base full snapshot
3. Apply full snapshot, then replay incremental deltas in order
4. Call `molt import` with the reconstructed bundle against the source installation
5. Existing data is not wiped before restore — `molt import` merge semantics apply
   (slug collision → error, requires `--rename` or `--overwrite`)

`--overwrite` flag on restore: wipe the destination group before importing.
Required for true point-in-time recovery. Default is merge (safer for partial
restores).

---

## Daemon behavior

The sync daemon (`molt sync start --daemonize`) runs as a background process:

- Writes PID to `~/.molt/sync.pid`; `molt sync stop` sends SIGTERM
- Logs to `~/.molt/sync.log` (tailed by `molt sync log`)
- Runs the sync on the configured interval using an internal ticker
- With `--on-change`: also watches source dirs with fsnotify and triggers
  an incremental sync within 30s of any write (debounced)
- On SIGHUP: re-reads config and resets the ticker without stopping
- On destination write failure: retries 3× with exponential backoff, then
  logs error and continues (does not crash)

Non-daemon mode (`molt sync start` without `--daemonize`) runs in the
foreground, logging to stderr. Useful for testing or running under a
process supervisor (launchd, systemd, supervisor).

### launchd plist (macOS)

`molt sync install` (planned) writes a launchd plist to
`~/Library/LaunchAgents/com.clawops.molt-sync.plist` and loads it.
`molt sync uninstall` removes and unloads it.

### systemd unit (Linux)

`molt sync install` writes a user systemd unit to
`~/.config/systemd/user/molt-sync.service` and enables it.

---

## Configuration file

`molt sync start` writes its config to `~/.molt/sync.json` on first run.
Subsequent invocations (including `molt sync now`) read from there.

```json
{
  "source_dir": "/Users/you/src/nanoclaw",
  "arch": "nanoclaw",
  "dest": "s3://my-bucket/nanoclaw-backup",
  "interval": "6h",
  "full_every": 7,
  "retain": 14,
  "on_change": false
}
```

Override any field at runtime by passing the corresponding flag — the flag
takes precedence over the config file but does not overwrite it.

---

## Status output

```
$ molt sync status

Daemon:    running (pid 12345)
Source:    /Users/you/src/nanoclaw  (nanoclaw 1.4.2)
Dest:      s3://my-bucket/nanoclaw-backup  ✓ reachable
Interval:  6h  (on-change: off)

Last run:  2026-03-27 06:00 EDT  —  incremental  142KB  2.1s  ✓
Next run:  2026-03-27 12:00 EDT  (in 3h 42m)

Snapshots: 8 stored  (oldest: 2026-03-23)  total 4.2MB
```

---

## ADR: Key design decisions

### Incremental over always-full

Full exports of large installations (many conversations, large session caches)
can be slow and expensive on remote destinations. Incremental reduces sync
time from minutes to seconds on typical runs. The `--full-every` floor ensures
the chain never grows unbounded and recovery always has a clean base.

### Reuse molt bundle format

Snapshots are valid `.molt` files. This means `molt inspect`, `molt diff`, and
`molt import` all work on sync snapshots without modification. No new format
needed. The only additions are the outer snapshot manifest and the `partial`
flag on incremental bundles.

### Daemon over cron

A daemon with `--on-change` support captures changes within 30s of a write,
which is much tighter than any practical cron interval. It also owns its own
retry logic, log, and status — no cron log archaeology needed. For users who
prefer cron, `molt sync now` is scriptable.

### Merge semantics on restore (not wipe)

Wiping the destination before restore is destructive and irreversible. Default
merge semantics mean a partial restore (one group, one session) is safe and
useful. `--overwrite` opts into the destructive path explicitly.

### Credentials from environment only

Storing AWS keys or SSH credentials in `~/.molt/sync.json` is a footgun.
`molt sync` reads credentials from the standard places each tool ecosystem
expects (AWS SDK, SSH agent) and documents this clearly. No credential
storage, no credential leaks.

---

## Driver protocol extension

`molt sync` uses a new `export_incremental_request` type. Drivers that don't
implement it return `{"type": "error", "code": "UNSUPPORTED"}`; `molt sync`
falls back to a full export.

### export_incremental_request

```json
{
  "type": "export_incremental_request",
  "source_dir": "/path/to/install",
  "since": "2026-03-26T06:00:00Z",
  "include": ["groups", "tasks", "skills"]
}
```

- `since` — ISO8601 timestamp; driver returns only items modified after this
- `include` — categories to export; omit sessions unless explicitly requested

### Response

Same as `export_request` (see DRIVER.md), with `partial: true` in the bundle
manifest and `base_since` echoed back.

---

## Out of scope (v0.1)

- Encryption at rest (`--encrypt`) — planned for v1
- Multi-source merge (sync from two installations into one dest) — planned for v1
- Cloud-native scheduling (Lambda, Cloud Run) — out of scope; use the daemon
- Conflict resolution on restore — merge semantics only for now; conflicts surface as slug collisions handled by existing molt import logic
