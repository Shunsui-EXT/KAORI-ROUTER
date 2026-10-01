# GitLab Duo Provider — Phase 1 (Design)

## Context

KAORI ROUTER is porting selected low-risk providers from CLIProxyAPIPlus
(a fork of CLIProxyAPI) one at a time, each on its own branch. Mistral
(API-key only, no OAuth) was ported first. GitLab Duo is the first OAuth
provider to port — audited earlier as low-risk (OAuth App registered by
the user themselves, official GitLab endpoints, no reverse-engineered
secrets, plus a personal-access-token alternative).

This is a full port of CLIProxyAPIPlus's GitLab Duo support, split into
two phases to keep each phase reviewable:

- **Phase 1 (this spec)**: OAuth + PAT login, token/metadata refresh, and
  the "native gateway" execution path — delegating to the already-existing
  Claude/Codex executors with GitLab's gateway credentials swapped in.
  This covers the common case: a GitLab account whose Duo-assigned model is
  Anthropic- or OpenAI-managed and whose `direct_access` metadata is
  available (true for OAuth logins on gitlab.com and most self-managed
  instances with Duo enabled).
- **Phase 2 (separate, later spec)**: the fallback text-prompt path
  (`requestChat`/`requestCodeSuggestions` against GitLab's older code-
  completion APIs) used only when native-gateway metadata isn't available,
  plus its manual SSE state machine. Phase 1 explicitly does not implement
  this fallback; a native-gateway-unavailable account simply gets an error
  in Phase 1, not a response, until Phase 2 lands.

## Goals

- `--gitlab-login` (OAuth, PKCE) and `--gitlab-token-login` (personal
  access token) both produce a working, persisted credential.
- A GitLab Duo account whose managed model is Anthropic- or OpenAI-backed
  (the common case) can serve real chat completions through KAORI ROUTER,
  non-streaming and streaming, with identical behavior to the equivalent
  reference code — because Phase 1 delegates to the existing Claude/Codex
  executors rather than reimplementing translation.
- Token refresh (OAuth refresh_token) and `direct_access` metadata refresh
  both happen automatically, matching the reference's refresh behavior.
- Model listing shows a stable `gitlab-duo` alias plus whatever model name
  GitLab's `direct_access` metadata currently reports — no hardcoded
  catalog, matching the reference's explicit design rationale (GitLab's
  model assignment changes without the client being told in advance).

## Non-goals (Phase 2)

- The `requestChat`/`requestCodeSuggestions` fallback path for accounts
  without usable `direct_access` metadata.
- The manual SSE state machine (`gitLabOpenAIStreamState`) that path needs.
- Any Management-API web-dashboard-triggered OAuth flow (the reference
  itself only documents the CLI flag flow; out of scope for both phases).

## Decisions

### File layout (mirrors the existing Devin provider's layout in this repo)

- `internal/auth/gitlab/gitlab_auth.go` — `AuthClient` (OAuth URL
  generation, code-for-token exchange, refresh, `GetCurrentUser`,
  `GetPersonalAccessTokenSelf`, `FetchDirectAccess`), `OAuthServer` (local
  callback HTTP server), `NormalizeBaseURL`, `TokenExpiry`. Ported from the
  reference's `internal/auth/gitlab/gitlab.go` essentially unchanged —
  same endpoints, same PKCE flow, same token/response shapes.
- `internal/auth/gitlab/pkce.go` — `GeneratePKCECodes`. The reference
  keeps this inline in its one `gitlab.go` file; splitting it into its own
  file matches this repo's own convention (every existing OAuth provider —
  Devin, Codex, Claude — has a separate `pkce.go`). Pure file-layout
  choice, identical logic.
- `sdk/auth/gitlab.go` — `GitLabAuthenticator` implementing this repo's
  `Authenticator` interface (`Provider() string`, `Login(ctx, cfg, opts)
  (*coreauth.Auth, error)`, `RefreshLead() *time.Duration`). This is a
  direct port of the reference's own `sdk/auth/gitlab.go` — the reference
  already uses the identical `Authenticator`/`manager.Login()` abstraction
  this repo uses, so this is not an adaptation, it's the same architecture
  in both repos. `Login()` dispatches on `opts.Metadata["login_mode"]`
  (`"oauth"` default, or `"pat"`) to `loginOAuth`/`loginPAT`, exactly as
  the reference does.
- `internal/cmd/gitlab_login.go` — `DoGitLabLogin` (sets `login_mode:
  "oauth"`) and `DoGitLabTokenLogin` (sets `login_mode: "pat"`), both
  calling `manager.Login(ctx, "gitlab", cfg, authOpts)`. Direct port of
  the reference's `internal/cmd/gitlab_login.go`.
- `internal/runtime/executor/gitlab_executor.go` — `GitLabExecutor`
  implementing `Identifier/Execute/ExecuteStream/Refresh/CountTokens/
  HttpRequest`, plus the native-gateway delegation functions
  (`nativeGateway`, `buildGitLabAnthropicGatewayAuth`,
  `buildGitLabOpenAIGatewayAuth`, `gitLabUsesAnthropicGateway`,
  `gitLabUsesOpenAIGateway`, `gitLabAnthropicGatewayBaseURL`,
  `gitLabOpenAIGatewayBaseURL`, `gitLabResolvedModel`, metadata helpers).
  Everything in the reference's 1383-line file that belongs to the
  fallback text-prompt path (`requestChat`, `requestCodeSuggestions`,
  `invokeText`, the `gitLabOpenAIStreamState` machinery, `buildGitLabPrompt`
  and friends) is excluded from this file in Phase 1.
- `GitLabModelsFromAuth` (model-discovery helper, ported from the
  reference) lives alongside the executor and is called from
  `sdk/cliproxy/service_models.go`'s `case "gitlab":`.

### Native-gateway delegation (the core Phase-1 mechanism)

`Execute`/`ExecuteStream` first call `nativeGateway(auth, req)`:

1. If `direct_access` metadata indicates the current model is
   Anthropic-managed (`gitLabUsesAnthropicGateway`) and both a gateway
   base-url and gateway token are present in `auth.Metadata`, clone `auth`,
   set `Provider: "claude"`, `Attributes["api_key"] = <gateway token>`,
   `Attributes["base_url"] = <GitLab's Anthropic proxy URL>`, copy any
   extra headers GitLab requires as `Attributes["header:"+name]`, and
   delegate the entire call to `NewClaudeExecutor(cfg)`.
2. Otherwise, if OpenAI-managed, same swap but `Provider: "codex"` and
   `NewCodexExecutor(cfg)`.
3. Otherwise (native gateway unavailable): Phase 1 returns an error here
   (`statusErr{code: http.StatusServiceUnavailable, msg: "gitlab duo:
   native gateway unavailable for this account; fallback path not yet
   implemented (Phase 2)"}`) instead of falling through to the reference's
   text-prompt fallback.

This reuses the existing Claude/Codex executors' request translation,
response translation, and streaming entirely unmodified — Phase 1 adds no
new translation logic, only the credential-swap plumbing and the
`direct_access` refresh that keeps the swapped-in token/base-url current.

### Auth flow detail

- **OAuth**: `GeneratePKCECodes()` → start local `OAuthServer` on the
  resolved callback port → open `GenerateAuthURL(...)` in the browser (or
  print it, if `--no-browser`) → wait for the callback → `
  ExchangeCodeForTokens` → `GetCurrentUser` for the account label →
  `FetchDirectAccess` for the initial gateway metadata → build and return
  the `coreauth.Auth` record with `Metadata` containing `access_token`,
  `refresh_token`, `oauth_client_id`, `oauth_client_secret`, `base_url`,
  plus the `direct_access` fields (`duo_gateway_token`,
  `duo_gateway_base_url`, `model_provider`, `model_name`, etc.).

  **Deviation from the reference (bug fix, not a port):** the reference's
  `loginOAuth` saves `oauth_client_id` to `Metadata` but never saves
  `oauth_client_secret` (confirmed by reading `sdk/auth/gitlab.go`
  end-to-end — `clientSecret` is used once for the token exchange call and
  then discarded). Its own executor's `refreshOAuthToken` reads
  `oauth_client_secret` back from `auth.Metadata` on every refresh, which
  is always empty there for any OAuth App that isn't the "public PKCE app,
  no secret" option — i.e. automatic token refresh is silently broken for
  confidential GitLab OAuth Apps in the reference. This port saves
  `oauth_client_secret` to `Metadata` alongside `oauth_client_id` so
  refresh works for both OAuth App types. Same category as the
  Magistral/Ministral reasoning-gate fix made during the Mistral port —
  fix a found bug rather than faithfully reproduce it.
- **PAT**: resolve the token from env var or prompt, call `GetCurrentUser`
  and `GetPersonalAccessTokenSelf` to validate it and get the account
  label, `FetchDirectAccess` for gateway metadata, build the `coreauth.Auth`
  record (no `refresh_token`/`oauth_client_id` needed — PATs are refreshed
  by the user outside this tool).
- **Credential input resolution** (`resolveString`/`requireInput`/
  `optionalInput`, ported from the reference unchanged): check
  `opts.Metadata` first, then environment variables (`GITLAB_BASE_URL`,
  `GITLAB_OAUTH_CLIENT_ID`, `GITLAB_OAUTH_CLIENT_SECRET`,
  `GITLAB_PERSONAL_ACCESS_TOKEN`), then an interactive prompt. No
  config.yaml section — confirmed unnecessary: `oauth_client_id`/
  `oauth_client_secret` are saved into the auth record's `Metadata` after
  first login and read from there on every subsequent refresh, so the
  env-var-or-prompt friction is paid once, not on every server start.
- **Callback port**: defaults to `17171` (`DefaultCallbackPort`, matching
  the reference exactly), overridable via the existing generic
  `--oauth-callback-port` flag. Fixed by design, not ephemeral: unlike
  Devin's hardcoded official client (which accepts any localhost port per
  native-app OAuth convention), a user-created GitLab OAuth App requires
  an exact `redirect_uri` match with no wildcard-port support — the user
  must know the port in advance to register it in their GitLab App
  settings.
- **Refresh** (`GitLabExecutor.Refresh`, called by the generic auth-
  refresh scheduler): refreshes the OAuth token if `gitLabOAuthTokenNeedsRefresh`
  says so (using the persisted `oauth_client_id`/`oauth_client_secret`),
  then re-fetches `direct_access` to keep the gateway token/base-url
  current (gateway tokens are short-lived per the reference's own design).
  PAT-based auths skip the OAuth-token-refresh step but still refresh
  `direct_access`.

### Config / wiring touchpoints

Unlike Mistral (a config-driven API-key list), GitLab Duo credentials are
OAuth-style file-backed auths (like Claude, Codex, Devin) — no
`gitlab-api-key` config array, no Management-API CRUD-list endpoints for
it. The wiring surface is:

- `sdk/auth/refresh_registry.go` — register the refresh-lead lookup for
  `"gitlab"` (mirrors every other OAuth provider's one-line registration).
- `sdk/cliproxy/service_executors.go` — `case "gitlab":
  s.coreManager.RegisterExecutor(executor.NewGitLabExecutor(cfg))`, and add
  `"gitlab"` to `baselineExecutorAuths()` (the gap found and fixed for
  Mistral — applying that lesson here from the start).
- `sdk/cliproxy/service_models.go` — `case "gitlab":` calling
  `GitLabModelsFromAuth(auth)` for dynamic model listing.
- `internal/cmd/main.go` (or wherever `--codex-login` etc. are already
  wired) — add `--gitlab-login` and `--gitlab-token-login` flags.
- `internal/api/handlers/management/auth_files*.go` — confirm GitLab auths
  show up in the existing generic auth-files list/delete/disable endpoints
  (these are provider-agnostic for file-backed auths, per Devin's own
  lack of special-casing there beyond its one web-dashboard-trigger file,
  which Phase 1 does not need).

## Verification plan

- Unit tests: PKCE generation (verifier/challenge shape), `TokenExpiry`
  calculation, `resolveString`/`requireInput`/`optionalInput` resolution
  order (metadata > env var > prompt > fallback), the three gateway-auth
  builder functions (`buildGitLabAnthropicGatewayAuth`/
  `buildGitLabOpenAIGatewayAuth`) producing a correctly-cloned `Auth` with
  the right `Provider`/`Attributes`.
- Integration tests (httptest): mock GitLab's `/oauth/token` and
  `/api/v4/code_suggestions/direct_access` endpoints plus a mock
  Anthropic-shaped and OpenAI-shaped gateway response, drive
  `GitLabExecutor.Execute`/`ExecuteStream` end-to-end and confirm the
  delegated Claude/Codex executor actually receives the swapped
  credentials and the response comes back correctly translated — the same
  rigor applied to Mistral's `/v1` base-url regression test.
- `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l` on every
  touched file — same bar as the Mistral port.
- A real OAuth login smoke test requires the user to register their own
  GitLab OAuth App and provide its client ID (and optionally secret) —
  this is explicitly a manual, user-driven verification step at the end,
  not something this plan can automate (no shared/public GitLab OAuth App
  exists to test against, unlike Mistral where a single API key sufficed).

## Risks / notes

- Phase 1's hard failure for native-gateway-unavailable accounts is an
  intentional, visible gap (clear error, not silent fallback) — a user
  whose GitLab Duo setup doesn't expose `direct_access` metadata will see
  an explicit "not yet implemented" error until Phase 2 ships, not a
  confusing failure.
- `direct_access` gateway tokens are short-lived (reference's own docs
  call this out — "automatic refresh of GitLab `direct_access` metadata").
  `Refresh()` must be wired into the same auto-refresh scheduler every
  other OAuth provider in this repo already uses, or long-running servers
  will see gateway requests start failing with expired tokens between
  logins.
- The native-gateway delegation clones `auth` and swaps `Provider`, which
  means usage/logging/cooldown bookkeeping keyed by `auth.Provider` or
  `auth.ID` needs checking during implementation — confirm the delegated
  Claude/Codex executor's usage reporting attributes correctly to the
  GitLab-origin credential, not silently to a phantom "claude"/"codex"
  entry, by reading how the reference's `nativeGateway` return value is
  consumed by callers beyond `Execute`/`ExecuteStream` (e.g. any usage or
  cooldown code keyed by the pre-swap `auth.ID`).
