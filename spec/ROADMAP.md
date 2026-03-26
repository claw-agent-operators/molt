# Molt Roadmap

## v0.1.0 — NanoClaw foundation ✓

- [x] Core molt binary (Go)
- [x] Bundle format implemented
- [x] `molt export` — NanoClaw source driver
- [x] `molt import` — NanoClaw target driver
- [x] `molt inspect` — preview bundle contents
- [x] `molt upgrade` — bundle version migration
- [x] `molt archs` — list installed drivers
- [x] Slug collision detection + `--rename` flag
- [x] `secrets-template.env` generation
- [x] `--dry-run` support
- [x] Session best-effort export

## v0.2.0 — Cross-arch (local)

- [ ] ZeptoClaw driver (OpenClaw → ZeptoClaw already exists; extend to NanoClaw source)
- [ ] OpenClaw driver (read existing OpenClaw installations)
- [ ] PicoClaw driver

## v0.3.0 — Walled gardens (remote drivers)

Major cloud and AI vendors have launched or announced hosted "claw" services.
These are SaaS deployments where you cannot access the underlying filesystem —
migration requires their API. Remote drivers handle this transparently via the
`config` field in the driver protocol.

- [ ] `molt-driver-anthropic-claw` — Anthropic hosted Claw (REST API)
- [ ] `molt-driver-cloudflare-claw` — Cloudflare Workers AI Claw
- [ ] `molt-driver-nvidia-claw` — NVIDIA AI Workbench Claw
- [ ] `--config key=value` flag for runtime auth passthrough
- [ ] `~/.molt/configs/<arch>.json` for persistent remote credentials
- [ ] `molt archs` shows `requires_config` fields for remote drivers

## v0.4.0 — Polish

- [ ] `molt diff <bundle1> <bundle2>` — show what changed between exports
- [ ] Incremental export (only export changes since last molt)
- [ ] Per-group exclude: `--exclude <slug>`
- [x] Shell completions (bash, zsh, fish)

## Future (v1+)

- [ ] Optional driver registry (ClawHub-style, opt-in) — see DRIVER.md
- [ ] `molt schedule` — scheduled/automatic exports as backup
- [ ] Encrypted bundles (`--encrypt`)
- [ ] Multi-source merge: import from two bundles into one installation
