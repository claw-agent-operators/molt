# Molt Roadmap

## v0.1.0 — NanoClaw foundation

- [ ] Core molt binary (Go or Rust)
- [ ] Bundle format implemented
- [ ] `molt export` — NanoClaw source driver
- [ ] `molt import` — NanoClaw target driver
- [ ] `molt upgrade` — bundle version migration
- [ ] `molt archs` — list installed drivers
- [ ] Slug collision detection + `--rename` flag
- [ ] `secrets-template.env` generation
- [ ] `--dry-run` support
- [ ] Session best-effort export

## v0.2.0 — Cross-arch

- [x] ZeptoClaw driver (export/import/probe; JSON-file based, no SQLite)
- [ ] OpenClaw driver (read existing OpenClaw installations)
- [ ] PicoClaw driver
- [ ] NanoBot driver (https://github.com/HKUDS/nanobot)
- [ ] Zeroclaw driver (https://github.com/zeroclaw-labs/zeroclaw)
- [ ] IronClaw driver (https://github.com/nearai/ironclaw)
- [ ] LobsterAI driver (https://github.com/netease-youdao/LobsterAI)
- [ ] TinyClaw driver (https://github.com/TinyAGI/tinyclaw)
- [ ] Moltis driver (https://github.com/moltis-org/moltis)
- [ ] CoPaw driver (https://github.com/agentscope-ai/CoPaw)
- [ ] EasyClaw driver (https://github.com/gaoyangz77/easyclaw)

## v0.3.0 — Polish

- [x] `molt diff <bundle1> <bundle2>` — show what changed between exports
- [x] Per-group exclude: `--exclude <slug>`
- [x] Per-group include: `--include <slug>` (mutually exclusive with --exclude)
- [x] Shell completions

## v1.0 — Sync and recovery

- [x] `molt sync` — scheduled backup daemon with incremental deltas (see [spec/SYNC.md](SYNC.md))
- [x] `molt restore` — point-in-time recovery from any saved bundle chain
- [x] Destination adapters: `file://`, `ssh://`
- [ ] Destination adapter: `s3://`
- [ ] `molt sync install` — launchd / systemd user unit

## Future

- [ ] Optional driver registry (ClawHub-style, opt-in)
- [ ] Encrypted bundles (`--encrypt`)
- [ ] Multi-source merge: import from two bundles into one installation
