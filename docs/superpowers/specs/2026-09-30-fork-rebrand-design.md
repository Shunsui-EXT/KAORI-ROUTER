# KAORI ROUTER — Fork & Rebrand of CLIProxyAPI (Design)

## Context

KAORI ROUTER is a self-hosted AI gateway for personal + small-community use.
After comparing candidate foundations (9Router, CLIProxyAPI, New API, and
alternatives), CLIProxyAPI (`github.com/router-for-me/CLIProxyAPI`, Go, MIT)
was chosen for its native support of pooling multiple OAuth subscription
accounts per provider (Claude Code, Codex, Gemini/Antigravity, Grok, Qwen,
Kimi, iFlow) with round-robin/load-balancing — the user's core requirement.

This spec covers **phase 1 only**: fork the upstream repo and rebrand its
identity. No feature changes. Additional features (new providers, quota
system, dashboard, etc.) are an explicitly separate, later phase.

## Goals

- Produce a working fork of CLIProxyAPI, identical in behavior, under the
  name/identity "KAORI ROUTER" / `KaoriRouter`.
- Preserve upstream git history and the ability to pull future upstream
  updates.
- Preserve MIT license attribution.

## Non-goals (explicitly deferred to phase 2)

- New providers/OAuth integrations beyond what upstream already supports.
- Dashboard/UI additions, per-user quota/access system, extra logging.
- Any change to runtime logic/behavior.

## Decisions

- **Source**: clone `https://github.com/router-for-me/CLIProxyAPI` with full
  git history.
- **Remotes**: `origin` = `https://github.com/Shunsui-EXT/KAORI-ROUTER`
  (auth via already-authenticated `gh` CLI, account `Shunsui-EXT`); `upstream`
  = `https://github.com/router-for-me/CLIProxyAPI` (kept for future
  `git fetch upstream` merges/cherry-picks).
- **Go module path**: `github.com/router-for-me/CLIProxyAPI/v8` →
  `github.com/Shunsui-EXT/KAORI-ROUTER` (drop the `/v8` major-version
  suffix — this fork starts its own versioning from scratch). Update
  `go.mod` and all internal import paths (~940 `.go` files) via scripted
  find/replace, not manual edits, then verify with a build.
- **Binary/identity name**: `CLIProxyAPI` → `KaoriRouter` (CamelCase) in
  code identifiers, binary name, Docker image/install paths. User-facing
  display string (TUI title, banners) becomes "KAORI ROUTER".
- **Docker/build files**: `Dockerfile`, `docker-compose.yml`,
  `docker-build.sh`, `docker-build.ps1` — update binary name and install
  paths only, no logic changes.
- **README.md**: rebrand title/badges/name; keep the existing documentation
  structure and content otherwise. `README_CN.md` and `README_JA.md` are
  deleted for this phase (can be redone later if multi-language docs are
  needed).
- **LICENSE**: keep both original MIT copyright lines (Luis Pater;
  Router-For.ME) — required by the MIT license — and add a new line:
  `Copyright (c) 2026-present Shunsui-EXT`.
- **`AGENTS.md` / `CLAUDE.md`** (repo-level AI-agent instructions): left
  unchanged; out of scope for a pure rebrand.
- **`.github/` CI workflows**: update only binary/image name references,
  no workflow logic changes.

## Verification plan

- `go build ./...` succeeds.
- `go vet ./...` clean.
- `go test ./...` passes (confirms rename didn't break anything).
- Run the built binary (`--help`/`--version`, or start the server) and
  confirm "KaoriRouter"/"KAORI ROUTER" branding appears correctly.
- Confirm `git remote -v` shows `origin` → Shunsui-EXT/KAORI-ROUTER and
  `upstream` → router-for-me/CLIProxyAPI.

## Risks / notes

- The ~940-file import-path rename is mechanical and must be scripted
  (grep + sed across `.go` files), not hand-edited, to avoid missed spots;
  correctness is verified by `go build ./...` after.
- `git clone` requires an empty target directory. Since this spec file is
  written into `/home/ubuntu/KAORI-ROUTER/docs/...` before the clone, the
  implementation clones upstream into a temporary directory first, then
  merges its contents (including `.git`) into `/home/ubuntu/KAORI-ROUTER`
  so this spec file is preserved and later committed on top of the
  imported upstream history.
- Future `git fetch upstream` merges may conflict with renamed import
  paths on every sync — an accepted tradeoff of forking, not solved here.
