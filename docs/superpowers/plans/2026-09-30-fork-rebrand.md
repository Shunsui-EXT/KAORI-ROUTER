# KAORI ROUTER Fork & Rebrand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a local, verified fork of CLIProxyAPI at `/home/ubuntu/KAORI-ROUTER`, rebranded to KAORI ROUTER, with identical runtime behavior and upstream git history preserved.

**Architecture:** Clone upstream with full history into a temp dir (git clone needs an empty target, and this project dir already holds the approved spec doc), merge into `/home/ubuntu/KAORI-ROUTER`, then apply the Go module path rename and the identity rebrand as separate, independently-verified commits on top of the imported history. Every provider-facing wire value, protocol constant, and cryptographic secret is left untouched by design — only strings that are purely local/display (console banners, TUI text, doc comments, README, build/Docker file names) are renamed. This preserves the spec's "identical behavior" goal.

**Tech Stack:** Go 1.26 (module `github.com/router-for-me/CLIProxyAPI/v8` → `github.com/Shunsui-EXT/KAORI-ROUTER`), git, gh CLI (already authenticated as `Shunsui-EXT`), Docker Compose config (no local Docker daemon available for build-testing — YAML syntax check only).

**Spec:** `docs/superpowers/specs/2026-09-30-fork-rebrand-design.md`

## Global Constraints

- No runtime/behavior changes — only identity/branding strings change (from the spec's Goals).
- Preserve full upstream git history; do not squash (from the spec's Decisions).
- MIT LICENSE must keep both original copyright lines (Luis Pater; Router-For.ME) verbatim (from the spec's Decisions — legally required).
- `AGENTS.md` and `CLAUDE.md` are not touched (from the spec's Decisions).
- Do not push to `origin` or `upstream` as part of this plan — push is a separate, explicitly user-confirmed action later (from the user's instruction).
- **Rebrand safety rule** (resolves ambiguity the spec doesn't spell out line-by-line): a string is safe to rename only if it is purely local/display — shown in this process's own console/log output, its own TUI, its own doc comments, or its own README/build files. A string is **left untouched** if it is transmitted over the network to any external host (HTTP headers, User-Agent values, mDNS/discovery protocol values), used as cryptographic key material, or asserted against in an existing test that isn't itself being changed in this plan. Task 3 below lists every affected file:line with an explicit rename/leave decision — do not rename anything not on the "rename" list, even if it contains "CLIProxyAPI".

## Review Focus

- **Module rename breaks the build silently in a subpackage the top-level `go build ./...` output truncates** — Task 2's step explicitly runs `go build ./... 2>&1 | tee` to a file and checks the exit code, not just skims stdout.
- **A provider-facing header/secret gets renamed by an overly broad find-and-replace** — Task 3 enumerates an explicit allow-list of files:lines to change; the step's own instructions say to change only those and nothing else, and a later step greps for the forbidden strings to prove they're untouched.
- **`examples/plugin/*` (separate Go modules, three languages) still reference the old upstream module path after Task 2** — this is a known, accepted gap for phase 1 (examples aren't part of the root module's `go build ./...`); Task 2's step notes this explicitly so it isn't mistaken for an oversight.
- **The go.sum / go.mod checksum drifts after the module rename**, causing `go build` to fail with a checksum or "missing go.sum entry" error — Task 2 runs `go mod tidy` right after the rename and re-verifies the build.
- **Docker/compose files reference a binary path that no longer exists after rename**, silently breaking the container even though no `docker build` is run to catch it locally (no Docker daemon in this environment) — Task 4's step cross-checks every changed path string against the actual binary output path from Task 2/3, and documents that a real `docker build` must be run once Docker is available, as a follow-up the user should do before deploying.

---

### Task 1: Bootstrap the fork with preserved history

**Files:**
- Create (via clone+merge): entire repository tree under `/home/ubuntu/KAORI-ROUTER`
- Preserve: `/home/ubuntu/KAORI-ROUTER/docs/superpowers/specs/2026-09-30-fork-rebrand-design.md` and this plan file itself

**Interfaces:**
- Produces: a git repo at `/home/ubuntu/KAORI-ROUTER` with `origin` = `https://github.com/Shunsui-EXT/KAORI-ROUTER`, `upstream` = `https://github.com/router-for-me/CLIProxyAPI`, full upstream history on `main`, and a working Go 1.26 toolchain — every later task depends on this.

- [ ] **Step 1: Install the Go 1.26 toolchain (not present on this machine)**

```bash
curl -fsSL https://go.dev/dl/go1.26.0.linux-amd64.tar.gz -o /tmp/go1.26.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go1.26.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
export PATH=$PATH:/usr/local/go/bin
```

- [ ] **Step 2: Verify the toolchain**

Run: `go version`
Expected: `go version go1.26.0 linux/amd64`

- [ ] **Step 3: Clone upstream with full history into a temp directory**

```bash
git clone https://github.com/router-for-me/CLIProxyAPI.git /tmp/kaori-router-upstream-clone
cd /tmp/kaori-router-upstream-clone && git log --oneline -1
```

Expected: clone succeeds, `git log` shows the latest upstream commit.

- [ ] **Step 4: Baseline-build the unmodified clone (establishes a known-good baseline before any renames)**

```bash
cd /tmp/kaori-router-upstream-clone
go mod download
go build ./... 2>&1 | tee /tmp/baseline-build.log
echo "exit code: $?"
```

Expected: exit code `0`, empty or warning-only output in `/tmp/baseline-build.log`. If this fails, stop — the problem is environmental (missing system deps like `build-essential` for CGO), not caused by anything in this plan, and must be resolved before continuing.

- [ ] **Step 5: Merge the clone into `/home/ubuntu/KAORI-ROUTER`, preserving the spec/plan docs already there**

```bash
rsync -a /tmp/kaori-router-upstream-clone/ /home/ubuntu/KAORI-ROUTER/
ls -la /home/ubuntu/KAORI-ROUTER/docs/superpowers/specs/
ls -la /home/ubuntu/KAORI-ROUTER/docs/superpowers/plans/
```

Expected: both the spec file and this plan file are still present (rsync merges into the non-empty target instead of failing the way `git clone` would); the `.git` directory with full upstream history is now inside `/home/ubuntu/KAORI-ROUTER`.

- [ ] **Step 6: Point `origin` at the KAORI ROUTER repo, add `upstream`, push the untouched history**

```bash
cd /home/ubuntu/KAORI-ROUTER
git remote set-url origin https://github.com/Shunsui-EXT/KAORI-ROUTER.git
git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git
git remote -v
```

Expected: `origin` → `Shunsui-EXT/KAORI-ROUTER`, `upstream` → `router-for-me/CLIProxyAPI`, both `(fetch)` and `(push)`.

- [ ] **Step 7: Commit the two doc files (currently untracked, since they were added after the clone's history) and confirm status is otherwise clean**

```bash
cd /home/ubuntu/KAORI-ROUTER
git status
git add docs/superpowers/specs/2026-09-30-fork-rebrand-design.md docs/superpowers/plans/2026-09-30-fork-rebrand.md
git commit -m "docs: add fork rebrand design spec and implementation plan"
```

Expected: `git status` before the add shows only those two files as untracked (everything else identical to upstream); commit succeeds.

---

### Task 2: Rename the Go module path

**Files:**
- Modify: `/home/ubuntu/KAORI-ROUTER/go.mod`
- Modify: every `.go` file under `cmd/`, `internal/`, `sdk/` that imports the old module path (confirmed count: 940 files; confirmed zero matches under `examples/`, which is intentionally out of scope — see Global Constraints)

**Interfaces:**
- Consumes: working toolchain and repo from Task 1.
- Produces: module path `github.com/Shunsui-EXT/KAORI-ROUTER` (no `/v8` suffix — this fork restarts its own versioning) that every later task's code references.

- [ ] **Step 1: Confirm the exact current module line and scope before changing anything**

```bash
cd /home/ubuntu/KAORI-ROUTER
head -1 go.mod
grep -rl "github.com/router-for-me/CLIProxyAPI/v8" --include="*.go" cmd internal sdk | wc -l
```

Expected: `module github.com/router-for-me/CLIProxyAPI/v8`; count `940`.

- [ ] **Step 2: Rename the module line in `go.mod`**

```bash
sed -i 's#^module github.com/router-for-me/CLIProxyAPI/v8$#module github.com/Shunsui-EXT/KAORI-ROUTER#' go.mod
head -1 go.mod
```

Expected: `module github.com/Shunsui-EXT/KAORI-ROUTER`.

- [ ] **Step 3: Rename every import path in `cmd/`, `internal/`, `sdk/`**

```bash
grep -rl "github.com/router-for-me/CLIProxyAPI/v8" --include="*.go" cmd internal sdk \
  | xargs sed -i 's#github.com/router-for-me/CLIProxyAPI/v8#github.com/Shunsui-EXT/KAORI-ROUTER#g'
grep -rl "github.com/router-for-me/CLIProxyAPI" --include="*.go" cmd internal sdk | wc -l
```

Expected: final count `0` (no remaining references to the old module path anywhere in the root module's source tree).

- [ ] **Step 4: Re-resolve dependencies and verify the build**

```bash
go mod tidy
go build ./... 2>&1 | tee /tmp/task2-build.log
echo "exit code: $?"
go vet ./... 2>&1 | tee /tmp/task2-vet.log
echo "exit code: $?"
```

Expected: both exit code `0`, no unexpected errors in either log (compare against `/tmp/baseline-build.log` from Task 1 Step 4 — any new error is caused by this rename).

- [ ] **Step 5: Run the existing test suite to confirm the rename didn't break anything**

```bash
go test ./... 2>&1 | tee /tmp/task2-test.log
echo "exit code: $?"
```

Expected: exit code `0`, same pass/fail profile as upstream (no new failures attributable to the rename — a failing test that already failed on unmodified upstream is a pre-existing issue, not something this task introduced; if unsure, run `go test ./...` against `/tmp/kaori-router-upstream-clone` too and diff the results).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd internal sdk
git commit -m "chore: rename Go module path to github.com/Shunsui-EXT/KAORI-ROUTER"
```

---

### Task 3: Rebrand local-display identity strings in Go source

**Files:**
- Modify: `cmd/server/main.go` (lines 85, 103, 638)
- Modify: `internal/util/provider.go` (line 1)
- Modify: `internal/tui/styles.go` (line 1)
- Modify: `internal/tui/i18n.go` (lines 86, 243)
- Modify: `internal/auth/devin/devin_auth.go` (the `loginSuccessHTML` constant)
- Modify: `internal/store/gitstore.go` (lines 1735-1736)
- Modify: `internal/runtime/executor/kimi_executor.go` (line 1158, comment only)
- Modify: `sdk/api/management.go` (line 1), `sdk/api/options.go` (line 1), `sdk/config/config.go` (line 4)
- **Do not modify** (provider-facing/protocol/crypto — see Global Constraints): `internal/managementasset/updater.go:32`, `internal/api/handlers/management/config_basic.go:23`, `internal/pluginstore/github.go:18`, `internal/auth/kimi/kimi.go:403`, `internal/runtime/executor/kimi_executor.go:1024-1025`, `internal/runtime/executor/claude_executor_request.go:1279`, `internal/runtime/executor/helps/antigravity_compaction.go:22,105`, all of `internal/discovery/` (mDNS protocol constant `ProductCPA`), `sdk/pluginstore/pluginstore.go:2` (references the name of a separate real upstream project, `CLIProxyAPIHome` — not this project's own identity), and all test files (no test asserts on any string being changed in this task).

**Interfaces:**
- Consumes: renamed module from Task 2.
- Produces: no new interfaces — pure string literal/comment changes.

- [ ] **Step 1: Rename the three console/log version banners in `cmd/server/main.go`**

Change (line 85):
```go
			fmt.Fprintf(os.Stderr, "CLIProxyAPI Version: %s, Commit: %s, BuiltAt: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
```
to:
```go
			fmt.Fprintf(os.Stderr, "KAORI ROUTER Version: %s, Commit: %s, BuiltAt: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
```

Change (line 103):
```go
		fmt.Printf("CLIProxyAPI Version: %s, Commit: %s, BuiltAt: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
```
to:
```go
		fmt.Printf("KAORI ROUTER Version: %s, Commit: %s, BuiltAt: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
```

Change (line 638):
```go
	log.Infof("CLIProxyAPI Version: %s, Commit: %s, BuiltAt: %s", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
```
to:
```go
	log.Infof("KAORI ROUTER Version: %s, Commit: %s, BuiltAt: %s", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
```

- [ ] **Step 2: Rename the two TUI title strings in `internal/tui/i18n.go`**

Change (line 86, Chinese locale):
```go
	"status_left":                 " CLIProxyAPI 管理终端",
```
to:
```go
	"status_left":                 " KAORI ROUTER 管理终端",
```

Change (line 243, English locale):
```go
	"status_left":                 " CLIProxyAPI Management TUI",
```
to:
```go
	"status_left":                 " KAORI ROUTER Management TUI",
```

- [ ] **Step 3: Rename the browser-facing OAuth success page text in `internal/auth/devin/devin_auth.go`**

Change:
```go
        <p>You have successfully logged in to Devin via CLIProxyAPI.</p>
```
to:
```go
        <p>You have successfully logged in to Devin via KAORI ROUTER.</p>
```

- [ ] **Step 4: Rename the doc comments in `internal/util/provider.go` and `internal/tui/styles.go`**

`internal/util/provider.go` line 1, change:
```go
// Package util provides utility functions used across the CLIProxyAPI application.
```
to:
```go
// Package util provides utility functions used across the KaoriRouter application.
```

`internal/tui/styles.go` line 1, change:
```go
// Package tui provides a terminal-based management interface for CLIProxyAPI.
```
to:
```go
// Package tui provides a terminal-based management interface for KaoriRouter.
```

- [ ] **Step 5: Rename the doc comments in the `sdk/` package files**

`sdk/api/management.go` line 1, change:
```go
// Package api exposes helpers for embedding CLIProxyAPI.
```
to:
```go
// Package api exposes helpers for embedding KaoriRouter.
```

`sdk/api/options.go` line 1, change:
```go
// Package api exposes server option helpers for embedding CLIProxyAPI.
```
to:
```go
// Package api exposes server option helpers for embedding KaoriRouter.
```

`sdk/config/config.go` line 4, change:
```go
// embed CLIProxyAPI without importing internal packages.
```
to:
```go
// embed KaoriRouter without importing internal packages.
```

- [ ] **Step 6: Rename the code comment in `internal/runtime/executor/kimi_executor.go`**

Change (line 1158):
```go
// It strips the CLIProxyAPI "kimi-" prefix and any Claude Code "[1m]" context
```
to:
```go
// It strips the KaoriRouter "kimi-" prefix and any Claude Code "[1m]" context
```

- [ ] **Step 7: Rename the internal git-store commit author identity in `internal/store/gitstore.go`**

Change (lines 1735-1736):
```go
	signature := &object.Signature{
		Name:  "CLIProxyAPI",
		Email: "cliproxy@local",
		When:  time.Now(),
	}
```
to:
```go
	signature := &object.Signature{
		Name:  "KaoriRouter",
		Email: "kaori@local",
		When:  time.Now(),
	}
```

- [ ] **Step 8: Prove the forbidden files were not touched**

```bash
cd /home/ubuntu/KAORI-ROUTER
for f in internal/managementasset/updater.go internal/api/handlers/management/config_basic.go internal/pluginstore/github.go internal/auth/kimi/kimi.go internal/runtime/executor/claude_executor_request.go internal/runtime/executor/helps/antigravity_compaction.go internal/discovery/types.go internal/discovery/spec.go internal/discovery/zeroconf.go sdk/pluginstore/pluginstore.go; do
  grep -n "CLIProxyAPI" "$f" || echo "WARNING: no CLIProxyAPI string found in $f — check it wasn't accidentally already changed"
done
grep -n "CLIProxyAPI" internal/runtime/executor/kimi_executor.go
```

Expected: every file in the first loop still prints its original "CLIProxyAPI" line(s) unchanged; the last command shows only lines 1024-1025 (`User-Agent`/`X-Msh-Platform` — untouched) since line 1158's comment was already renamed in Step 6.

- [ ] **Step 9: Build, vet, and test**

```bash
go build ./... 2>&1 | tee /tmp/task3-build.log; echo "exit code: $?"
go vet ./... 2>&1 | tee /tmp/task3-vet.log; echo "exit code: $?"
go test ./... 2>&1 | tee /tmp/task3-test.log; echo "exit code: $?"
```

Expected: all three exit code `0`, same pass/fail profile as Task 2 Step 5.

- [ ] **Step 10: Commit**

```bash
git add cmd/server/main.go internal/util/provider.go internal/tui/styles.go internal/tui/i18n.go internal/auth/devin/devin_auth.go internal/store/gitstore.go internal/runtime/executor/kimi_executor.go sdk/api/management.go sdk/api/options.go sdk/config/config.go
git commit -m "rebrand: rename local-display identity strings from CLIProxyAPI to KAORI ROUTER"
```

---

### Task 4: Rebrand Docker/build files

**Files:**
- Modify: `Dockerfile`
- Modify: `docker-compose.yml`
- Modify: `docker-build.sh`
- Modify: `docker-build.ps1`

**Interfaces:**
- Consumes: binary name `KaoriRouter` established conceptually in Task 3 (the binary itself is still literally produced as `./CLIProxyAPI` by the Dockerfile's `go build -o` flag today — this task is what actually renames the build output file name).

- [ ] **Step 1: Rename the build output binary name and install path in `Dockerfile`**

Change:
```dockerfile
RUN CGO_ENABLED=1 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" -o ./CLIProxyAPI ./cmd/server/

FROM debian:bookworm

RUN apt-get update && apt-get install -y --no-install-recommends tzdata ca-certificates && rm -rf /var/lib/apt/lists/*

RUN mkdir /CLIProxyAPI

COPY --from=builder ./app/CLIProxyAPI /CLIProxyAPI/CLIProxyAPI

COPY config.example.yaml /CLIProxyAPI/config.example.yaml

WORKDIR /CLIProxyAPI
```
to:
```dockerfile
RUN CGO_ENABLED=1 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" -o ./KaoriRouter ./cmd/server/

FROM debian:bookworm

RUN apt-get update && apt-get install -y --no-install-recommends tzdata ca-certificates && rm -rf /var/lib/apt/lists/*

RUN mkdir /KaoriRouter

COPY --from=builder ./app/KaoriRouter /KaoriRouter/KaoriRouter

COPY config.example.yaml /KaoriRouter/config.example.yaml

WORKDIR /KaoriRouter
```

Also check the remainder of the file (the `CMD`/`ENTRYPOINT` line) for any other `CLIProxyAPI` reference and apply the same rename:

```bash
grep -n "CLIProxyAPI" Dockerfile
```

Rename every remaining match the same way (binary/path name only).

- [ ] **Step 2: Rename the image/container identity and mount paths in `docker-compose.yml`**

Change:
```yaml
services:
  cli-proxy-api:
    image: ${CLI_PROXY_IMAGE:-eceasy/cli-proxy-api:latest}
    pull_policy: always
    build:
      context: .
      dockerfile: Dockerfile
      args:
        VERSION: ${VERSION:-dev}
        COMMIT: ${COMMIT:-none}
        BUILD_DATE: ${BUILD_DATE:-unknown}
    container_name: cli-proxy-api
```
to:
```yaml
services:
  kaori-router:
    image: ${CLI_PROXY_IMAGE:-shunsuiext/kaori-router:latest}
    pull_policy: always
    build:
      context: .
      dockerfile: Dockerfile
      args:
        VERSION: ${VERSION:-dev}
        COMMIT: ${COMMIT:-none}
        BUILD_DATE: ${BUILD_DATE:-unknown}
    container_name: kaori-router
```

And update the volume mount targets to match Task 4 Step 1's new `/KaoriRouter` path:
```yaml
    volumes:
      - ${CLI_PROXY_CONFIG_PATH:-./config.yaml}:/KaoriRouter/config.yaml
      - ${CLI_PROXY_AUTH_PATH:-./auths}:/root/.cli-proxy-api
      - ${CLI_PROXY_LOG_PATH:-./logs}:/KaoriRouter/logs
      - ${CLI_PROXY_PLUGIN_PATH:-./plugins}:/KaoriRouter/plugins
```

Leave the `CLI_PROXY_*` environment **variable names** themselves unchanged (`CLI_PROXY_IMAGE`, `CLI_PROXY_CONFIG_PATH`, etc.) and leave `/root/.cli-proxy-api` unchanged — renaming env var names is a config-surface change beyond "pure rebrand" scope per the spec's non-goals, and `/root/.cli-proxy-api` is an unrelated auth-state directory name, not the binary install path.

- [ ] **Step 3: Rename the local-build image tag in `docker-build.sh`**

Change:
```bash
    export CLI_PROXY_IMAGE="cli-proxy-api:local"
```
to:
```bash
    export CLI_PROXY_IMAGE="kaori-router:local"
```

- [ ] **Step 4: Rename the local-build image tag in `docker-build.ps1`**

```bash
grep -n "cli-proxy-api" docker-build.ps1
```

Change the matched line (`$env:CLI_PROXY_IMAGE = "cli-proxy-api:local"`) to:
```powershell
        $env:CLI_PROXY_IMAGE = "kaori-router:local"
```

- [ ] **Step 5: Verify YAML syntax (no Docker daemon available in this environment to do a real build)**

```bash
python3 -c "import yaml; yaml.safe_load(open('docker-compose.yml')); print('docker-compose.yml: OK')"
```

Expected: `docker-compose.yml: OK`. Note in the commit message / final report that an actual `docker build .` should be run by the user once Docker is available, before relying on this in production — this environment has no Docker daemon to verify the image actually builds.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile docker-compose.yml docker-build.sh docker-build.ps1
git commit -m "rebrand: rename Docker/build artifacts from cli-proxy-api to kaori-router"
```

---

### Task 5: Rebrand README, remove translations

**Files:**
- Modify: `README.md` (title, intro paragraph only — rest of content structure preserved per spec)
- Delete: `README_CN.md`
- Delete: `README_JA.md`

**Interfaces:**
- Consumes: nothing from prior tasks (pure documentation).

- [ ] **Step 1: Rewrite the title, language-switcher line, and intro paragraph**

Change:
```markdown
# CLI Proxy API

English | [中文](README_CN.md) | [日本語](README_JA.md)

If you want to use CLIProxyAPI on your desktop, we recommend our [EasyCLIProxyAPI](https://github.com/router-for-me/EasyCLIProxyAPI) desktop client. It provides a graphical configuration UI, automatic updates, system tray integration, and one-click start/stop for the CLIProxyAPI service.

CLIProxyAPI is a proxy server that provides OpenAI/Gemini/Claude/Codex/Grok compatible API interfaces for CLI.
```
to:
```markdown
# KAORI ROUTER

KAORI ROUTER is a self-hosted AI gateway, forked from [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). It is a proxy server that provides OpenAI/Gemini/Claude/Codex/Grok compatible API interfaces for CLI, and pools multiple OAuth subscription accounts per provider.
```

This removes the language-switcher line (the two translated files are deleted in Step 2) and removes the `EasyCLIProxyAPI` desktop-client recommendation paragraph (that companion project is upstream's own separate repo, not part of this fork). Leave every other line of `README.md` (provider tables, configuration docs, feature descriptions further down the file — including any further "CLIProxyAPI" mentions in prose or affiliate-tracking URLs) exactly as-is; the spec calls for the documentation content structure to be preserved, and rewriting every downstream mention is out of scope for this phase.

- [ ] **Step 2: Delete the translated READMEs**

```bash
rm README_CN.md README_JA.md
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git rm README_CN.md README_JA.md
git commit -m "rebrand: rewrite README title/intro, drop translated READMEs"
```

---

### Task 6: Update LICENSE

**Files:**
- Modify: `LICENSE`

**Interfaces:**
- Consumes: nothing.

- [ ] **Step 1: Add the new copyright line, keeping both original lines verbatim**

Change:
```
MIT License

Copyright (c) 2025-2005.9 Luis Pater
Copyright (c) 2025.9-present Router-For.ME
```
to:
```
MIT License

Copyright (c) 2025-2005.9 Luis Pater
Copyright (c) 2025.9-present Router-For.ME
Copyright (c) 2026-present Shunsui-EXT
```

- [ ] **Step 2: Confirm the rest of the license body (permission/warranty text) is byte-for-byte unchanged**

```bash
diff <(tail -n +6 LICENSE) <(tail -n +5 /tmp/kaori-router-upstream-clone/LICENSE)
```

Expected: no output (identical).

- [ ] **Step 3: Commit**

```bash
git add LICENSE
git commit -m "docs: add Shunsui-EXT copyright line to LICENSE"
```

---

### Task 7: Update CI workflow branding references

**Files:**
- Modify: `.github/workflows/release.yaml`
- Modify: `.github/workflows/docker-image.yml`

**Interfaces:**
- Consumes: nothing (no logic changes — only the archive/image name strings).

- [ ] **Step 1: Rename the release archive name pattern in `release.yaml`**

```bash
grep -c "CLIProxyAPI" .github/workflows/release.yaml
sed -i 's/CLIProxyAPI_/KaoriRouter_/g' .github/workflows/release.yaml
grep -c "CLIProxyAPI" .github/workflows/release.yaml
```

Expected: first count `26` (matches the earlier repo-wide grep of this file), second count `0`. This only changes the archive filename prefix used in `archive_name=`, `--pattern`, and `assets=(...)` lines — no job logic, triggers, or steps are touched.

- [ ] **Step 2: Rename the Docker image app name in `docker-image.yml`**

Change:
```yaml
  APP_NAME: CLIProxyAPI
```
to:
```yaml
  APP_NAME: KaoriRouter
```

- [ ] **Step 3: Verify YAML syntax for both files**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yaml')); print('release.yaml: OK')"
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/docker-image.yml')); print('docker-image.yml: OK')"
```

Expected: both print `OK`.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yaml .github/workflows/docker-image.yml
git commit -m "rebrand: rename CI archive/image name references to KaoriRouter"
```

---

### Task 8: Final full verification

**Files:** none modified — this task only runs checks.

**Interfaces:** none.

- [ ] **Step 1: Full clean build, vet, and test from scratch**

```bash
cd /home/ubuntu/KAORI-ROUTER
go clean -cache
go build -o /tmp/kaori-router-bin ./cmd/server/
echo "build exit code: $?"
go vet ./...
echo "vet exit code: $?"
go test ./...
echo "test exit code: $?"
```

Expected: all three exit code `0`.

- [ ] **Step 2: Confirm the branding actually appears at runtime**

```bash
/tmp/kaori-router-bin --version 2>&1 || /tmp/kaori-router-bin -h 2>&1 | head -5
```

Expected: output contains `KAORI ROUTER Version:` (from Task 3 Step 1). If `--version`/`-h` isn't the right flag, check `cmd/server/main.go`'s flag definitions for the correct one — do not guess; read the file.

- [ ] **Step 3: Confirm git remotes and clean working tree**

```bash
git remote -v
git status
git log --oneline -8
```

Expected: `origin` → `Shunsui-EXT/KAORI-ROUTER` (fetch+push), `upstream` → `router-for-me/CLIProxyAPI` (fetch+push); working tree clean; log shows this plan's commits (docs, module rename, identity rebrand, Docker rebrand, README, LICENSE, CI) sitting on top of imported upstream history.

- [ ] **Step 4: Report status to the user — do not push**

Summarize: build/vet/test results, confirmed branding output, confirmed remotes, and that pushing to `origin`/`upstream` was intentionally left out of this plan pending the user's explicit go-ahead (per Global Constraints).
