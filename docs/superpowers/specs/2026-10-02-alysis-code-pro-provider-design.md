# Alysis Code Pro Provider — Design

## Context

KAORI ROUTER is porting selected providers from CLIProxyAPIPlus (a community
fork of CLIProxyAPI) one at a time, each on its own branch, following a deep
audit of 9 candidate providers. Alysis Code Pro was selected first — it was
the only candidate the audit classified as "Medium risk, leaning Safe" with
"Small" port complexity: a clean device-flow login against the vendor's own
Supabase backend, no client-fingerprint spoofing, no reverse-engineered
private endpoints, and (unlike the abandoned GitLab Duo port) the caller can
select the underlying model directly.

Alysis Code Pro (`alysiscode.com`) is a boutique hosted gateway that proxies
to DeepSeek models (DeepSeek V4 Flash/Pro/Vision-exp) through an
OpenAI-compatible API, metering the user's subscription credits server-side.

## Goals

- `--alysis-login` (CLI) and a management-API device-flow endpoint
  (`GET /v0/management/alysis-auth-url`) both produce a working, persisted
  credential — a long-lived gateway key (`slk_...`).
- A TUI (`--tui`) entry for Alysis, using the existing generic
  `oauthProvider{deviceFlow: true}` device-flow UI — no new TUI plumbing.
- Chat completions (streaming and non-streaming) work end-to-end against
  the real `alysiscode.com` gateway, with the client able to select any
  model the gateway's live `/models` catalog reports (or the static
  3-model fallback catalog when that fetch fails).
- No refresh scheduling needed — the gateway key is long-lived and the
  reference confirms there is no server-side refresh/rotation endpoint for
  it, so `Refresh()` is a no-op (matches Mistral's pattern) and
  `RefreshLead()` returns `nil` (matches Devin's and the reference's own
  `AlysisAuthenticator`).

## Non-goals

- No API-key-array config.yaml entry (unlike Mistral). This credential is
  always obtained via the device-flow login, not pasted into config by the
  user — mirroring the reference's own design (there is no
  `alysis-api-key` config array in the reference either).
- No change to how any other provider's login/wiring works.

## Decisions

### File layout (mirrors Devin's auth-kind layout + Mistral's executor simplicity)

- `internal/auth/alysis/alysis_auth.go` — `Auth` client: `InitiateDeviceFlow`,
  `PollForToken` (and its internal `pollOnce` helper), the Supabase
  endpoint constants (`ProductSiteURL`, `DeviceCodePath`, `DeviceTokenPath`,
  `GatewayPathPrefix`), the public Supabase anon key constant, and the
  `SupabaseURL` variable (kept as a `var`, not `const`, exactly like the
  reference, so tests can point it at a local stub server). Ported
  essentially unchanged from the reference — same endpoints, same device-
  flow semantics, same public anon key (it is a public client identifier
  for a public endpoint, not a secret; `verify_jwt` is disabled
  server-side for these two routes by Alysis's own design).
- `internal/auth/alysis/alysis_token.go` — `TokenStorage` struct
  (`Key`, `Email`, `Type` fields) implementing this repo's existing
  `baseauth.TokenStorage` interface (`SaveTokenToFile(authFilePath
  string) error`, confirmed present at `sdk/cliproxy/auth` — the exact
  interface the reference's `TokenStorage` also implements), plus
  `CredentialFileName(email string) string`.
- `sdk/auth/alysis.go` — `AlysisAuthenticator` implementing this repo's
  `sdk/auth.Authenticator` interface (`Provider() string`, `Login(ctx,
  cfg, opts) (*coreauth.Auth, error)`, `RefreshLead() *time.Duration`).
  `Login()` drives the device flow (print the verification URL + user
  code, poll until approved/denied/expired), then builds a `coreauth.Auth`
  with `Metadata["gatewayKey"]` set to the approved key, `Storage` set to
  the `TokenStorage` instance (so the generic save path persists the
  `{"gatewayKey":...,"email":...,"type":"alysis"}` JSON shape), and
  `Attributes["auth_kind"] = "oauth"` (classifies it as OAuth-kind for the
  generic model-capability/routing machinery, matching the reference and
  matching how this repo already classifies Devin/GitLab).
- `internal/cmd/alysis_login.go` — `DoAlysisLogin(cfg, options)`, calling
  `manager.Login(ctx, "alysis", cfg, authOpts)` through the generic
  `newAuthManager()` — never referencing `AlysisAuthenticator` directly,
  matching every other provider's `internal/cmd/*_login.go` file.
- `internal/runtime/executor/alysis_executor.go` — `AlysisExecutor`
  implementing `cliproxyauth.ProviderExecutor`
  (`Identifier/Execute/ExecuteStream/Refresh/CountTokens/HttpRequest`),
  built on this repo's actual proven helpers — confirmed by reading
  `internal/runtime/executor/mistral_executor.go` line-by-line as the
  template, NOT the reference's exact helper calls (the reference uses
  several package-local lowercase helpers —
  `newProxyAwareHTTPClient`, `newUsageReporter`, `recordAPIRequest`,
  `parseOpenAIUsage`, `parseOpenAIStreamUsage` — that do not exist under
  those names in this repo; this repo's equivalents live under the
  exported `helps` package: `helps.NewProxyAwareHTTPClient`,
  `helps.NewUsageReporter`, `helps.RecordAPIRequest`,
  `helps.RecordAPIResponseMetadata`, `helps.RecordAPIResponseError`,
  `helps.AppendAPIResponseChunk`, `helps.ParseOpenAIUsage`,
  `helps.ParseOpenAIStreamUsage` — all confirmed present and exactly this
  shape by reading Mistral's executor, which already uses every one of
  them). The reference's extra layers (`payloadRequestedModel`,
  `applyPayloadConfigWithRoot`, `thinking.ApplyThinking`) are NOT used —
  Mistral's own executor doesn't use them either, and Alysis's DeepSeek
  models need no equivalent special-casing, so this port follows Mistral's
  proven simpler shape rather than reimplementing reference-only layers
  this repo doesn't have.
- Model catalog: a `GetAlysisModels()` static 3-entry fallback (DeepSeek
  V4 Flash/Pro/Flash-Vision-Exp) added alongside this repo's other
  `GetXxxModels()` functions in `internal/registry/model_definitions.go`
  (matching where `GetMistralModels()` lives in this repo, rather than the
  reference's separate `alysis_models.go` file — follow this repo's own
  convention, not the reference's file-splitting choice), plus a
  `FetchAlysisModels(ctx, auth, cfg)` live-fetch-and-merge function in the
  executor file, ported from the reference's version essentially
  unchanged (merge live `/models` entries into the static fallback,
  keeping the static entries as a floor when the live fetch fails or
  returns fewer entries).

### Credential storage and the executor's Bearer-token path

`alysisCredentials(auth)` reads `auth.Metadata["gatewayKey"]` (string),
falling back to `auth.Attributes["gatewayKey"]` only defensively (the
reference's own `Login()` never actually populates the Attributes copy —
this fallback branch is dead in practice today, carried over for parity
with the reference rather than removed, since it costs nothing and matches
the reference's own defensive shape). The executor sets
`Authorization: Bearer <gatewayKey>` on every request — no signing, no
per-request credential rotation, no refresh.

### Management API endpoint and TUI entry

- `GET /v0/management/alysis-auth-url` is added to
  `internal/api/handlers/management/`, following the shape of this repo's
  existing device-flow management endpoints for other `deviceFlow: true`
  providers (e.g. Kimi, xAI, Meta) — register an OAuth session (so the
  frontend can poll/cancel), start the device flow, return the
  verification URL + user code immediately, then poll for the token in a
  background goroutine and persist the resulting `coreauth.Auth` once
  approved. This mirrors the reference's own `RequestAlysisToken` handler
  shape, adapted to this repo's existing session-registration helpers
  (`RegisterOAuthSession`, `IsOAuthSessionPending`,
  `watchOAuthSessionCancel`, `guardOAuthSessionPendingForSave` — confirmed
  these exist in this repo already, since Kimi/xAI/Meta's device-flow
  endpoints already use them).
- `cmd/server/main.go`'s management route registration gets one new line:
  `mgmt.GET("/alysis-auth-url", s.mgmt.RequestAlysisToken)`, next to the
  other device-flow provider routes.
- `internal/tui/oauth_tab.go`'s `oauthProviders` slice gets one new entry:
  `{"Alysis Code Pro", "alysis-auth-url", "<emoji>", true}` — the
  `deviceFlow: true` flag means the existing generic device-flow UI
  (poll/cancel/countdown) handles it with no additional per-provider TUI
  code, confirmed by reading how Kimi/xAI/Meta's entries work today.

### CLI flag and dual auth-manager registration (the GitLab lesson)

- `cmd/server/main.go` gets `--alysis-login` wired exactly like
  `--devin-login`/`--gitlab-login`: a `gitlabLogin`-shaped boolean var,
  `flag.BoolVar`, the non-server-mode `commandMode` gate, the dispatch
  arm, and the `argvEnablesBoolFlag` boolean-flag case list.
- `AlysisAuthenticator` is registered in **both**
  `internal/cmd/auth_manager.go`'s `newAuthManager()` (CLI/management-API
  login path) **and** `sdk/cliproxy/service_auth.go`'s
  `newDefaultAuthManager()` (the server's own auth manager, used by the
  refresh scheduler and any other internal consumer of the generic
  authenticator set). The reference itself only registers Alysis in the
  former, not the latter — the same gap this project found and fixed for
  GitLab Duo (`internal/cmd/auth_manager.go` vs
  `sdk/cliproxy/service_auth.go`), caught there only through a live test
  that failed with "authenticator not registered." For Alysis specifically
  the functional impact of leaving it out would be smaller (`RefreshLead()`
  returns `nil`, so nothing would try to schedule a refresh for it), but
  this port registers it in both places anyway, deliberately not
  reproducing the reference's own inconsistency.
- `sdk/auth/refresh_registry.go` gets a `registerRefreshLead("alysis",
  func() Authenticator { return NewAlysisAuthenticator() })` line, matching
  every other provider, even though `RefreshLead()` returns `nil` for this
  provider (consistency with the existing registration pattern, not a
  functional requirement).

### Config / wiring touchpoints

- `sdk/cliproxy/service_executors.go` — `case "alysis":
  s.coreManager.RegisterExecutor(executor.NewAlysisExecutor(cfg))`, and add
  `"alysis"` to `baselineExecutorAuths()` (the gap found and fixed for
  Mistral, applied here from the start).
- `sdk/cliproxy/service_models.go` — `case "alysis":` calling
  `executor.FetchAlysisModels(ctx, auth, cfg)` for model listing. Confirmed
  by reading the file: this provider needs the `case "devin":`-style shape
  (OAuth-kind, no config-driven API-key list) rather than the
  `case "mistral":`-style shape (no `resolveConfigAlysisKey` lookup needed).
- `internal/api/handlers/management/auth_files*.go` — confirm Alysis auths
  show up in the existing generic auth-files list/delete/disable endpoints
  (these are provider-agnostic for file-backed auths; no special-casing
  expected, matching Devin's and GitLab's own lack of special-casing there).

## Data flow

**CLI login (`--alysis-login`):** `DoAlysisLogin` → `manager.Login(ctx,
"alysis", cfg, opts)` → `AlysisAuthenticator.Login` → `alysis.NewAuth()` →
`InitiateDeviceFlow` (POST `/functions/v1/device-code` with the public
Supabase anon key) → print verification URL + user code → `PollForToken`
(POST `/functions/v1/device-token` every 5s, immediate first probe) until
`status == "approved"` (carries the `slk_...` key), `"denied"` (abort,
clear error), or `"expired"/"not_found"/"already_claimed"` (abort, clear
error) → build `coreauth.Auth` with `Metadata["gatewayKey"]` and `Storage`
set → saved to the auth directory by the generic manager.

**Management-API login (`GET /v0/management/alysis-auth-url`):** same
device-flow mechanics, but driven by the HTTP handler directly (register an
OAuth session for poll/cancel, return the verification URL + user code to
the caller immediately, poll in a background goroutine, persist on
approval) — matching how Kimi/xAI/Meta's device-flow management endpoints
already work in this repo.

**Request execution:** `AlysisExecutor.Execute`/`ExecuteStream` reads
`auth.Metadata["gatewayKey"]`, builds a plain Bearer-token request against
`https://vzigujbcjjmpntxhmyvr.supabase.co/functions/v1/llm/v1/chat/completions`,
translates the incoming request (`sdktranslator.TranslateRequest`,
source format → `"openai"`) and the response back (`TranslateNonStream`/
`TranslateStream`), reports usage via `helps.NewUsageReporter` +
`helps.ParseOpenAIUsage`/`ParseOpenAIStreamUsage`, exactly matching
Mistral's proven request/response shape.

## Error handling

- Device-flow `denied`/`expired`/`not_found`/`already_claimed` statuses
  each produce a specific, readable error message (not a generic "login
  failed") — ported directly from the reference's own `pollOnce` switch.
- Gateway HTTP errors (4xx/5xx from `alysiscode.com`) surface as
  `statusErr{code, msg}` carrying the real upstream status code and body,
  matching Mistral's pattern — not swallowed or generically rewrapped.
- Missing/empty `gatewayKey` on a credential produces a clear
  `statusErr{code: http.StatusUnauthorized, msg: "alysis: missing gateway
  key"}` before any request is attempted, matching Mistral's
  missing-API-key guard.

## Verification plan

- Unit tests: device-flow state transitions (`InitiateDeviceFlow`,
  `PollForToken`'s approved/denied/expired/pending branches) against an
  `httptest.Server` standing in for the Supabase endpoints (`SupabaseURL`
  is a `var` specifically so tests can redirect it, matching the
  reference's own test setup).
- Integration tests (httptest): mock the gateway's `/chat/completions` and
  `/models` endpoints, drive `AlysisExecutor.Execute`/`ExecuteStream`
  end-to-end, confirm the Bearer token and translated payload reach the
  mock correctly and the response translates back correctly — the same
  rigor applied to Mistral and GitLab's own integration tests.
- `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l` on every
  touched file — same bar as every prior provider port.
- **Mandatory live verification before merge (explicit user instruction):**
  after implementation, review, and a clean build/test pass, do **not**
  merge to `main`. The user will run a real device-flow login (approve the
  code at `alysiscode.com/activate` with their own account) and send a real
  chat-completion request through KAORI ROUTER to the live Alysis gateway.
  Only merge to `main` after the user explicitly confirms this live test
  succeeded — mirroring the verification approach used for GitLab Duo,
  minus that port's eventual outcome (GitLab Duo was reverted for
  unrelated architectural reasons after merge; this plan's gate is placed
  *before* merge specifically to decide merge-worthiness with live
  evidence rather than after).

## Risks / notes

- Vendor durability: Alysis Code Pro is a small, boutique hosted gateway,
  not an established major vendor. If the service shuts down or changes
  its API, this provider could stop working with no advance notice — a
  business-continuity risk, not a technical or legal one. This was already
  flagged in the original audit and is not expected to block the port
  itself; it's a reason to prefer the live-verification-before-merge gate
  this spec already requires.
- The public Supabase anon key is committed to source as a constant
  (`AnonKey`), exactly as the reference does. This is confirmed to be a
  non-secret, publicly-shippable client identifier for the two device-flow
  endpoints specifically (the vendor disabled `verify_jwt` for them by
  design) — it is **not** equivalent to a per-user secret and does not
  grant access to any user's gateway key or chat data on its own. Still,
  per this project's standing security hygiene, this key must never be
  conflated with or logged alongside a real user's `slk_...` gateway key.
- `baseModel`/model-name handling: unlike Mistral (which has
  `resolveMistralModelName` to map a config-defined alias to an upstream
  name), Alysis has no config-driven key list, so there is no equivalent
  alias-resolution step — the requested model name passes straight through
  to the upstream `model` field, matching the reference's own behavior.
