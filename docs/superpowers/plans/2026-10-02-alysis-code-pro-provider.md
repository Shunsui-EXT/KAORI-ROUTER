# Alysis Code Pro Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Alysis Code Pro as a provider in KAORI ROUTER — device-flow login (CLI, management-API, TUI) producing a long-lived gateway key, and request execution via a simple OpenAI-compatible Bearer-token executor against `alysiscode.com`'s DeepSeek-backed gateway.

**Architecture:** A new `internal/auth/alysis` package implements the RFC-8628-style device-flow HTTP client against Alysis's public Supabase edge functions. A new `AlysisAuthenticator` in `sdk/auth` implements this repo's existing `Authenticator` interface (same one Devin/XAI/Meta use) to produce a persisted `coreauth.Auth` record holding the gateway key. A new `AlysisExecutor` in `internal/runtime/executor` implements `cliproxyauth.ProviderExecutor`, following `MistralExecutor`'s exact proven shape (not the reference's package-local helpers, which don't exist in this repo) — plain Bearer-token requests, no refresh, no signing.

**Tech Stack:** Go 1.26, existing KAORI ROUTER device-flow conventions (`sdk/auth.Authenticator`, `sdk/cliproxy/auth.ProviderExecutor`, `internal/api/handlers/management`'s existing XAI/Meta/Kimi device-flow endpoints), `net/http/httptest` for integration tests.

**Spec:** `docs/superpowers/specs/2026-10-02-alysis-code-pro-provider-design.md`

## Global Constraints

- No `config.yaml` API-key array for this provider. The gateway key is
  always obtained via the device-flow login (CLI, management-API, or
  TUI) and persisted as a file-backed `coreauth.Auth`, never pasted by
  the user into config — this provider is OAuth-kind, not API-key-kind.
- `AlysisAuthenticator.RefreshLead()` returns `nil`; `AlysisExecutor.Refresh()`
  returns the `auth` unchanged — the gateway key is long-lived with no
  server-side refresh/rotation endpoint.
- The executor MUST use this repo's actual proven helpers under the
  `helps` package (`helps.NewProxyAwareHTTPClient`, `helps.NewUsageReporter`,
  `helps.RecordAPIRequest`, `helps.RecordAPIResponseMetadata`,
  `helps.RecordAPIResponseError`, `helps.AppendAPIResponseChunk`,
  `helps.ParseOpenAIUsage`, `helps.ParseOpenAIStreamUsage`) — confirmed
  by reading `internal/runtime/executor/mistral_executor.go`, which
  already uses every one of them. Do NOT use the reference's
  package-local lowercase names (`newProxyAwareHTTPClient`,
  `newUsageReporter`, etc.) — those do not exist in this repo.
- The static fallback model catalog (`GetAlysisModels()`) lives in
  `internal/registry/model_definitions.go` (confirmed: `GetMistralModels()`
  lives there at line 541), not a separate `alysis_models.go` file.
- `AlysisAuthenticator` MUST be registered in BOTH
  `internal/cmd/auth_manager.go`'s `newAuthManager()` (CLI/management-API
  login path) AND `sdk/cliproxy/service_auth.go`'s `newDefaultAuthManager()`
  (the server's own auth manager) — the reference itself only registers
  it in the former; this is the exact gap found and fixed for GitLab Duo
  earlier in this project via a failed live test ("authenticator not
  registered"). Do not reproduce that gap here.
- `sdk/cliproxy/service_models.go`'s `case "alysis":` must follow the
  `case "devin":` shape (no config-key resolution step) — confirmed:
  `case "devin": models = registry.GetDevinModels(); models =
  applyExcludedModels(models, excluded)` at line 167 — not the
  `case "mistral":` shape, which resolves a config array entry.
- Every new/touched Go file must pass `gofmt -l` with no output,
  `go vet ./...` clean, and `go build ./...` clean.
- **Do NOT merge this branch to `main` as part of this plan's execution.**
  The user will perform a real device-flow login (approving the code at
  `alysiscode.com/activate` with their own account) and send a real chat
  completion through KAORI ROUTER to the live Alysis gateway. Merging to
  `main` happens only after the user explicitly confirms that live test
  succeeded — this is a manual gate outside this plan's automation, not
  a task to execute.

## Review Focus

- A reasonable person running `--alysis-login` and clicking "deny" (or
  letting the code expire, or reusing an already-claimed code) on the
  website expects a clear, specific error ("login was rejected", "code
  expired") — not a generic "login failed" or a hang. Both the CLI path
  and the management-API path must surface the specific status.
- A reasonable person whose stored Alysis credential is somehow missing
  its `gatewayKey` (corrupted file, manual edit) expects a clear
  "missing gateway key" error from the very first request — not a
  request sent upstream with an empty/missing `Authorization` header
  that produces a confusing 401 from Alysis's own servers.
- A reasonable person hitting a real Alysis-side error (rate limit,
  insufficient credits, invalid model) expects the real upstream status
  code and message to come through — not a generic 500 or a swallowed
  error.
- A reasonable person whose live `/models` fetch fails (network blip,
  Alysis API hiccup) expects model listing to still work via the static
  3-model fallback — not an empty model list or a hard error.
- A reasonable person who cancels (Ctrl+C) a `--alysis-login` while it's
  polling expects the process to exit promptly, not hang until the
  device code's own expiry timeout — `PollForToken` must respect
  context cancellation at every poll tick, not just the initial probe.

---

### Task 1: Alysis device-flow auth client (`internal/auth/alysis`)

**Files:**
- Create: `internal/auth/alysis/alysis_auth.go`
- Create: `internal/auth/alysis/alysis_token.go`
- Test: `internal/auth/alysis/alysis_auth_test.go`

**Interfaces:**
- Produces: `alysis.ProductSiteURL`, `alysis.DeviceCodePath`,
  `alysis.DeviceTokenPath`, `alysis.GatewayPathPrefix`, `alysis.AnonKey`
  (constants), `alysis.SupabaseURL` (var, overridable in tests),
  `alysis.DeviceCodeResponse{DeviceCode, UserCode, VerificationURL,
  VerificationURLComplete, ExpiresIn, Interval}`,
  `alysis.DeviceTokenResponse{Status, Key}`, `alysis.NewAuth() *Auth`
  with methods `InitiateDeviceFlow(ctx) (*DeviceCodeResponse, error)`,
  `PollForToken(ctx, deviceCode string) (*DeviceTokenResponse, error)`,
  `alysis.TokenStorage{Key, Email, Type string}` implementing
  `SaveTokenToFile(authFilePath string) error` (matches
  `sdk/cliproxy/auth.TokenStorage` interface — confirmed present at
  `sdk/cliproxy/auth/token_storage.go` or similar, interface has exactly
  this one method), `alysis.CredentialFileName(email string) string`.
  Task 2 consumes this package directly.

- [ ] **Step 1: Write the failing PKCE-free device-flow test**

```go
// internal/auth/alysis/alysis_auth_test.go
package alysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInitiateDeviceFlow_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DeviceCodePath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("apikey") != AnonKey {
			t.Errorf("expected apikey header to be the anon key")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:              "dc-123",
			UserCode:                "ABCD-1234",
			VerificationURLComplete: "https://alysiscode.com/activate?code=ABCD-1234",
			ExpiresIn:               900,
			Interval:                5,
		})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	grant, err := auth.InitiateDeviceFlow(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant.DeviceCode != "dc-123" || grant.UserCode != "ABCD-1234" {
		t.Errorf("unexpected grant: %+v", grant)
	}
}

func TestPollForToken_ApprovedOnFirstProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DeviceTokenPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "approved", Key: "slk_test123"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	status, err := auth.PollForToken(context.Background(), "dc-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Key != "slk_test123" {
		t.Errorf("expected key slk_test123, got %q", status.Key)
	}
}

func TestPollForToken_Denied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "denied"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	_, err := auth.PollForToken(context.Background(), "dc-123")
	if err == nil {
		t.Fatal("expected an error when login is denied")
	}
}

func TestPollForToken_Expired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "expired"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	_, err := auth.PollForToken(context.Background(), "dc-123")
	if err == nil {
		t.Fatal("expected an error when the device code has expired")
	}
}

func TestPollForToken_RespectsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "pending"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	auth := NewAuth()
	_, err := auth.PollForToken(ctx, "dc-123")
	if err == nil {
		t.Fatal("expected PollForToken to respect an already-cancelled context")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/auth/alysis/... -v`
Expected: FAIL — `package alysis is not in GOROOT` / `undefined: NewAuth`
(the package doesn't exist yet).

- [ ] **Step 3: Implement the device-flow auth client**

```go
// internal/auth/alysis/alysis_auth.go

// Package alysis provides authentication and token management for the
// Alysis Code Pro hosted service (`alysiscode.com`).
//
// Login follows an RFC 8628-style device flow against Supabase Edge
// Functions: the CLI requests a short user code, the user approves it on
// the website's /activate page, and the CLI polls until it receives a
// long-lived gateway key ("slk_..."). The gateway is an OpenAI-compatible
// proxy that meters the subscription's credits server-side.
package alysis

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Default endpoints for the Alysis Code hosted backend. They mirror the
// constants in the reference CLI (pypi alysis-code, src/alysis_code/
// alysis_cloud.py) so both clients talk to the same deployment.
const (
	// ProductSiteURL is the product site serving the /activate approval page.
	ProductSiteURL = "https://alysiscode.com"

	// DeviceCodePath starts a device login (returns user_code + device_code).
	DeviceCodePath = "/functions/v1/device-code"

	// DeviceTokenPath exchanges a device_code for the gateway key once the
	// user approves it on /activate.
	DeviceTokenPath = "/functions/v1/device-token"

	// GatewayPathPrefix is the OpenAI-compatible gateway base path.
	GatewayPathPrefix = "/functions/v1/llm/v1"

	// AnonKey is the Supabase public anon key shipped in the reference CLI.
	// It is a public client identifier: the device endpoints are public by
	// design and verify_jwt is disabled server-side for them.
	AnonKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InZ6aWd1amJjamptcG50eGhteXZyIiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODA5Mzc0NTIsImV4cCI6MjA5NjUxMzQ1Mn0." +
		"vLH9q-BNO8IWIZrVlvCw8pZWXdLgmKG4Tl9toTTD3pg"
)

// SupabaseURL is the Supabase project hosting the device-login edge
// functions. It is a variable so tests can point it at a local stub.
var SupabaseURL = "https://vzigujbcjjmpntxhmyvr.supabase.co"

// DeviceCodeResponse is the payload returned by POST device-code.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURL         string `json:"verification_url"`
	VerificationURLComplete string `json:"verification_url_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceTokenResponse is the payload returned by POST device-token.
type DeviceTokenResponse struct {
	Status string `json:"status"`
	Key    string `json:"key"`
}

// Auth drives the Alysis Code device login flow.
type Auth struct {
	client *http.Client
}

// NewAuth creates an Auth instance with the default HTTP client.
func NewAuth() *Auth {
	return &Auth{client: &http.Client{Timeout: 30 * time.Second}}
}

// InitiateDeviceFlow starts the device flow and returns the grant.
func (a *Auth) InitiateDeviceFlow(ctx context.Context) (*DeviceCodeResponse, error) {
	body, err := a.postJSON(ctx, DeviceCodePath, strings.NewReader(`{"client_name":"kaori-router"}`))
	if err != nil {
		return nil, err
	}
	var grant DeviceCodeResponse
	if err = json.Unmarshal(body, &grant); err != nil {
		return nil, fmt.Errorf("alysis device flow: invalid device-code response: %w", err)
	}
	if strings.TrimSpace(grant.DeviceCode) == "" || strings.TrimSpace(grant.UserCode) == "" {
		return nil, fmt.Errorf("alysis device flow: device-code response missing codes")
	}
	if grant.ExpiresIn <= 0 {
		grant.ExpiresIn = 900
	}
	if grant.Interval <= 0 {
		grant.Interval = 5
	}
	return &grant, nil
}

// PollForToken polls device-token until the user approves the code. HTTP and
// status semantics mirror the reference CLI: status "approved" carries the
// gateway key, "denied" and "expired"/"not_found"/"already_claimed" abort,
// anything else keeps polling. Polling always respects ctx cancellation.
func (a *Auth) PollForToken(ctx context.Context, deviceCode string) (*DeviceTokenResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deviceCode == "" {
		return nil, fmt.Errorf("alysis device flow: device code is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Probe immediately so a just-approved code does not wait a full interval.
	if status, done, err := a.pollOnce(ctx, deviceCode); done || err != nil {
		return status, err
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			status, done, err := a.pollOnce(ctx, deviceCode)
			if err != nil || done {
				return status, err
			}
		}
	}
}

func (a *Auth) pollOnce(ctx context.Context, deviceCode string) (*DeviceTokenResponse, bool, error) {
	payload := fmt.Sprintf(`{"device_code":%q}`, deviceCode)
	body, err := a.postJSON(ctx, DeviceTokenPath, strings.NewReader(payload))
	if err != nil {
		return nil, true, err
	}
	var resp DeviceTokenResponse
	if err = json.Unmarshal(body, &resp); err != nil {
		return nil, true, fmt.Errorf("alysis device flow: invalid device-token response: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(resp.Status)) {
	case "approved":
		if strings.TrimSpace(resp.Key) == "" {
			return nil, true, fmt.Errorf("alysis device flow: approved response missing key")
		}
		return &resp, true, nil
	case "denied":
		return nil, true, fmt.Errorf("alysis device flow: login was rejected on the website")
	case "expired", "not_found", "already_claimed":
		return nil, true, fmt.Errorf("alysis device flow: login code expired")
	default: // "pending" or unknown: keep waiting.
		return nil, false, nil
	}
}

// postJSON POSTs a JSON payload to a Supabase edge function with the public
// anon key headers. The device endpoints are public by design; the gateway
// (chat/models) calls use per-user slk_ keys instead.
func (a *Auth) postJSON(ctx context.Context, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SupabaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", AnonKey)
	req.Header.Set("Authorization", "Bearer "+AnonKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alysis device flow: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("alysis device flow: failed to read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("alysis device flow: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
```

Note the one deliberate deviation from the reference: `PollForToken` adds
an `if err := ctx.Err(); err != nil { return nil, err }` guard at the top
(the reference lacks this), so an already-cancelled context returns
immediately instead of still performing one HTTP probe first — this is
what Review Focus item 5 and `TestPollForToken_RespectsContextCancellation`
require.

```go
// internal/auth/alysis/alysis_token.go

// Package alysis provides authentication and token management for the
// Alysis Code Pro hosted service.
package alysis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/misc"
	log "github.com/sirupsen/logrus"
)

// TokenStorage stores the gateway key persisted after a device-flow login.
type TokenStorage struct {
	// Key is the long-lived gateway key (``slk_...``) used as the Bearer
	// credential against the OpenAI-compatible gateway.
	Key string `json:"gatewayKey"`

	// Email is the account email recorded at login time (best effort).
	Email string `json:"email"`

	// Type indicates the authentication provider type, always "alysis".
	Type string `json:"type"`
}

// SaveTokenToFile serializes the token storage to a JSON file.
func (ts *TokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "alysis"
	if err := os.MkdirAll(filepath.Dir(authFilePath), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	f, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			log.Errorf("failed to close file: %v", errClose)
		}
	}()

	if err = json.NewEncoder(f).Encode(ts); err != nil {
		return fmt.Errorf("failed to write token to file: %w", err)
	}
	return nil
}

// CredentialFileName returns the filename used to persist alysis credentials.
func CredentialFileName(email string) string {
	if email == "" {
		email = "account"
	}
	return fmt.Sprintf("alysis-%s.json", email)
}
```

Verify `internal/misc.LogSavingCredentials` exists before using it —
confirmed present at `internal/misc/credentials.go:16`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/auth/alysis/... -v`
Expected: PASS — all 5 test functions green.

- [ ] **Step 5: gofmt, build, commit**

```bash
gofmt -w internal/auth/alysis/alysis_auth.go internal/auth/alysis/alysis_token.go internal/auth/alysis/alysis_auth_test.go
go build ./internal/auth/alysis/... && echo "build OK"
git add internal/auth/alysis/
git commit -m "feat: add Alysis Code Pro device-flow auth client and token storage"
```

---

### Task 2: AlysisAuthenticator (`sdk/auth/alysis.go`)

**Files:**
- Create: `sdk/auth/alysis.go`
- Create: `sdk/auth/alysis_test.go`
- Modify: `sdk/auth/refresh_registry.go`

**Interfaces:**
- Consumes: Task 1's `alysis` package (`alysis.NewAuth`,
  `alysis.ProductSiteURL`, `alysis.TokenStorage`,
  `alysis.CredentialFileName`), this repo's existing
  `sdk/auth.LoginOptions{NoBrowser, CallbackPort, Metadata
  map[string]string, Prompt}` and `sdk/auth.Authenticator` interface
  (`sdk/auth/interfaces.go`).
- Produces: `auth.AlysisAuthenticator` (exported type),
  `auth.NewAlysisAuthenticator() *AlysisAuthenticator`. Task 3 consumes
  this only indirectly via `manager.Login(ctx, "alysis", cfg, opts)` —
  it never references `AlysisAuthenticator` directly, matching every
  other provider's login command.

- [ ] **Step 1: Write the failing authenticator test**

```go
// sdk/auth/alysis_test.go
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
)

func TestAlysisAuthenticator_Provider(t *testing.T) {
	a := NewAlysisAuthenticator()
	if a.Provider() != "alysis" {
		t.Errorf("expected provider %q, got %q", "alysis", a.Provider())
	}
}

func TestAlysisAuthenticator_RefreshLead_IsNil(t *testing.T) {
	a := NewAlysisAuthenticator()
	if a.RefreshLead() != nil {
		t.Error("expected RefreshLead to be nil: the gateway key is long-lived with no refresh endpoint")
	}
}

func TestAlysisAuthenticator_Login_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case alysis.DeviceCodePath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceCodeResponse{
				DeviceCode: "dc-xyz", UserCode: "WXYZ-9876",
				VerificationURLComplete: "https://alysiscode.com/activate?code=WXYZ-9876",
				ExpiresIn: 900, Interval: 5,
			})
		case alysis.DeviceTokenPath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceTokenResponse{Status: "approved", Key: "slk_abc123"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	originalURL := alysis.SupabaseURL
	alysis.SupabaseURL = server.URL
	defer func() { alysis.SupabaseURL = originalURL }()

	a := NewAlysisAuthenticator()
	record, err := a.Login(context.Background(), &config.Config{}, &LoginOptions{Metadata: map[string]string{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Provider != "alysis" {
		t.Errorf("expected provider %q, got %q", "alysis", record.Provider)
	}
	gatewayKey, _ := record.Metadata["gatewayKey"].(string)
	if gatewayKey != "slk_abc123" {
		t.Errorf("expected gatewayKey %q in metadata, got %q", "slk_abc123", gatewayKey)
	}
	if record.Storage == nil {
		t.Error("expected Storage to be set so the generic manager can persist the credential")
	}
}

func TestAlysisAuthenticator_Login_Denied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case alysis.DeviceCodePath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceCodeResponse{
				DeviceCode: "dc-denied", UserCode: "DENY-0000", ExpiresIn: 900, Interval: 5,
			})
		case alysis.DeviceTokenPath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceTokenResponse{Status: "denied"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	originalURL := alysis.SupabaseURL
	alysis.SupabaseURL = server.URL
	defer func() { alysis.SupabaseURL = originalURL }()

	a := NewAlysisAuthenticator()
	_, err := a.Login(context.Background(), &config.Config{}, &LoginOptions{Metadata: map[string]string{}})
	if err == nil {
		t.Fatal("expected an error when the device-flow login is denied")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sdk/auth/... -run TestAlysisAuthenticator -v`
Expected: FAIL — `undefined: NewAlysisAuthenticator`.

- [ ] **Step 3: Implement `AlysisAuthenticator`**

```go
// sdk/auth/alysis.go
package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	coreauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
)

// AlysisAuthenticator implements the device-flow login for Alysis Code Pro.
type AlysisAuthenticator struct{}

// NewAlysisAuthenticator constructs an Alysis authenticator.
func NewAlysisAuthenticator() *AlysisAuthenticator {
	return &AlysisAuthenticator{}
}

func (a *AlysisAuthenticator) Provider() string {
	return "alysis"
}

func (a *AlysisAuthenticator) RefreshLead() *time.Duration {
	return nil
}

// Login runs the Alysis Code device flow: request a user code, have the user
// approve it on https://alysiscode.com/activate, then persist the gateway key.
func (a *AlysisAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	authSvc := alysis.NewAuth()

	fmt.Println("Initiating Alysis device authentication...")
	grant, err := authSvc.InitiateDeviceFlow(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate device flow: %w", err)
	}

	verificationURL := grant.VerificationURLComplete
	if verificationURL == "" {
		verificationURL = grant.VerificationURL
	}
	if verificationURL == "" {
		verificationURL = alysis.ProductSiteURL + "/activate"
	}
	fmt.Printf("Please visit: %s\n", verificationURL)
	fmt.Printf("And approve code: %s\n", grant.UserCode)

	fmt.Println("Waiting for authorization...")
	status, err := authSvc.PollForToken(ctx, grant.DeviceCode)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	fmt.Println("Alysis Code Pro authentication successful.")

	email := ""
	if opts.Metadata != nil {
		email = opts.Metadata["email"]
	}
	ts := &alysis.TokenStorage{
		Key:   status.Key,
		Type:  "alysis",
		Email: email,
	}

	fileName := alysis.CredentialFileName(ts.Email)
	metadata := map[string]any{
		"type":       "alysis",
		"gatewayKey": status.Key,
		"email":      ts.Email,
		"auth_kind":  "oauth",
	}

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Label:    "Alysis Code Pro",
		Storage:  ts,
		Metadata: metadata,
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./sdk/auth/... -run TestAlysisAuthenticator -v`
Expected: PASS — all 4 test functions green.

- [ ] **Step 5: Register the refresh lead**

**File:** Modify `sdk/auth/refresh_registry.go` — add next to the existing
`registerRefreshLead("meta", ...)` line:

```go
registerRefreshLead("alysis", func() Authenticator { return NewAlysisAuthenticator() })
```

- [ ] **Step 6: gofmt, build, test, commit**

```bash
gofmt -w sdk/auth/alysis.go sdk/auth/alysis_test.go sdk/auth/refresh_registry.go
go build ./sdk/auth/... && echo "build OK"
go test ./sdk/auth/... -v 2>&1 | tail -20
git add sdk/auth/alysis.go sdk/auth/alysis_test.go sdk/auth/refresh_registry.go
git commit -m "feat: add AlysisAuthenticator (device-flow login)"
```

---

### Task 3: CLI login command, flag wiring, and dual auth-manager registration

**Files:**
- Create: `internal/cmd/alysis_login.go`
- Modify: `cmd/server/main.go` (flag declaration, dispatch, gating lists)
- Modify: `internal/cmd/auth_manager.go`
- Modify: `sdk/cliproxy/service_auth.go`

**Interfaces:**
- Consumes: `newAuthManager()` and `LoginOptions` (already used by every
  other `internal/cmd/*_login.go` file — the exact pattern is in
  `internal/cmd/devin_login.go`, confirmed read in full).
- Produces: `cmd.DoAlysisLogin(cfg *config.Config, options
  *LoginOptions)`, CLI flag `--alysis-login`.

- [ ] **Step 1: Implement the login command**

```go
// internal/cmd/alysis_login.go
package cmd

import (
	"context"
	"fmt"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	sdkAuth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoAlysisLogin triggers the Alysis Code Pro device-flow login and saves credentials.
func DoAlysisLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser:    options.NoBrowser,
		CallbackPort: options.CallbackPort,
		Metadata:     map[string]string{},
	}

	record, savedPath, err := manager.Login(context.Background(), "alysis", cfg, authOpts)
	if err != nil {
		log.Errorf("Alysis authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("Alysis Code Pro authentication successful!")
}
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./internal/cmd/... 2>&1 | head -20`
Expected: clean (no output).

- [ ] **Step 3: Register `AlysisAuthenticator` in both auth-manager wiring sites**

**File:** `internal/cmd/auth_manager.go` — add
`sdkAuth.NewAlysisAuthenticator(),` to the `sdkAuth.NewManager(...)` call
list, next to `sdkAuth.NewMetaAuthenticator(),`:

```go
func newAuthManager() *sdkAuth.Manager {
	store := sdkAuth.GetTokenStore()
	manager := sdkAuth.NewManager(store,
		sdkAuth.NewCodexAuthenticator(),
		sdkAuth.NewClaudeAuthenticator(),
		sdkAuth.NewAntigravityAuthenticator(),
		sdkAuth.NewKimiAuthenticator(),
		sdkAuth.NewKimiAIAuthenticator(),
		sdkAuth.NewKimiAIDotAuthenticator(),
		sdkAuth.NewXAIAuthenticator(),
		sdkAuth.NewDevinAuthenticator(),
		sdkAuth.NewMetaAuthenticator(),
		sdkAuth.NewAlysisAuthenticator(),
	)
	return manager
}
```

**File:** `sdk/cliproxy/service_auth.go` — add the exact same line to
`newDefaultAuthManager()`:

```go
func newDefaultAuthManager() *sdkAuth.Manager {
	return sdkAuth.NewManager(
		sdkAuth.GetTokenStore(),
		sdkAuth.NewCodexAuthenticator(),
		sdkAuth.NewClaudeAuthenticator(),
		sdkAuth.NewAntigravityAuthenticator(),
		sdkAuth.NewKimiAuthenticator(),
		sdkAuth.NewKimiAIAuthenticator(),
		sdkAuth.NewKimiAIDotAuthenticator(),
		sdkAuth.NewXAIAuthenticator(),
		sdkAuth.NewDevinAuthenticator(),
		sdkAuth.NewMetaAuthenticator(),
		sdkAuth.NewAlysisAuthenticator(),
	)
}
```

This is the exact gap found and fixed for GitLab Duo earlier in this
project — both sites must be updated, not just one.

- [ ] **Step 4: Wire the `--alysis-login` CLI flag in `cmd/server/main.go`**

Read the file around the existing `devinLogin`/`--devin-login` wiring
(confirmed at these exact lines in the current file):
- `cmd/server/main.go:116` — `var devinLogin bool`
- `cmd/server/main.go:145` — `flag.BoolVar(&devinLogin, "devin-login", false, "Login to Devin using OAuth")`
- `cmd/server/main.go:657` — `commandMode := vertexImport != "" || antigravityLogin || codexLogin || codexDeviceLogin || claudeLogin || kimiLogin || kimiAILogin || xaiLogin || devinLogin || metaLogin`
- `cmd/server/main.go:733` — `} else if devinLogin {` (dispatch, inside the larger `if/else if` chain)
- `cmd/server/main.go:971` — `"antigravity-login", "kimi-login", "kimi-ai-login", "xai-login", "devin-login", "meta-login",` (the `argvEnablesBoolFlag` boolean-flag case list)

Add `alysisLogin`'s equivalent at every one of these 5 sites, following
the exact same pattern:

1. Declare `var alysisLogin bool` next to `var devinLogin bool`.
2. Add `flag.BoolVar(&alysisLogin, "alysis-login", false, "Login to Alysis Code Pro using a device-flow")` next to the Devin flag registration.
3. Add `|| alysisLogin` to the `commandMode` boolean chain.
4. Add a new arm `} else if alysisLogin { cmd.DoAlysisLogin(cfg, options)` to the dispatch chain, right after the `devinLogin`/before the `metaLogin` arm (or anywhere in that same `if/else if` chain — order among arms doesn't matter, they're mutually exclusive).
5. Add `"alysis-login"` to the `argvEnablesBoolFlag` case list string.

Since the line numbers above may have shifted slightly from edits in
other recently-merged work, re-grep `devinLogin` in this file yourself
first (`grep -n devinLogin cmd/server/main.go`) to confirm the exact
current line numbers before editing — don't trust these numbers blindly
if the grep shows something different.

- [ ] **Step 5: Build, smoke-test, verify, commit**

```bash
gofmt -w internal/cmd/alysis_login.go internal/cmd/auth_manager.go sdk/cliproxy/service_auth.go cmd/server/main.go
go build -o /tmp/kaori-alysis-test ./cmd/server && \
  /tmp/kaori-alysis-test --help 2>&1 | grep -A1 alysis && rm /tmp/kaori-alysis-test
go build ./... && echo "FULL BUILD OK"
go test ./internal/cmd/... ./cmd/... 2>&1 | tail -10
git add internal/cmd/alysis_login.go internal/cmd/auth_manager.go sdk/cliproxy/service_auth.go cmd/server/main.go
git commit -m "feat: add --alysis-login CLI flag and dual auth-manager registration"
```

Expected `--help` output includes:
```
  -alysis-login
    Login to Alysis Code Pro using a device-flow
```

---

### Task 4: Alysis executor and model catalog

**Files:**
- Create: `internal/runtime/executor/alysis_executor.go`
- Test: `internal/runtime/executor/alysis_executor_test.go`
- Test: `internal/runtime/executor/alysis_executor_integration_test.go`
- Modify: `internal/registry/model_definitions.go`

**Interfaces:**
- Consumes: Task 1's `alysis` package (`alysis.SupabaseURL`,
  `alysis.GatewayPathPrefix`), this repo's existing
  `helps.NewProxyAwareHTTPClient`, `helps.NewUsageReporter`,
  `helps.RecordAPIRequest`, `helps.UpstreamRequestLog`,
  `helps.RecordAPIResponseMetadata`, `helps.RecordAPIResponseError`,
  `helps.AppendAPIResponseChunk`, `helps.ParseOpenAIUsage`,
  `helps.ParseOpenAIStreamUsage` (all confirmed present and used by
  `internal/runtime/executor/mistral_executor.go`), `util.ApplyCustomHeadersFromAttrs`,
  `thinking.ParseSuffix`, `statusErr{code, msg}` (package-level type in
  this same `executor` package), `cliproxyauth.Auth.AccountInfo()
  (string, string)`.
- Produces: `executor.NewAlysisExecutor(cfg *config.Config)
  *AlysisExecutor` implementing `cliproxyauth.ProviderExecutor`,
  `executor.FetchAlysisModels(ctx, auth *cliproxyauth.Auth, cfg
  *config.Config) []*registry.ModelInfo`,
  `registry.GetAlysisModels() []*registry.ModelInfo`. Task 6 consumes
  both `NewAlysisExecutor` and `FetchAlysisModels` directly.

- [ ] **Step 1: Add the static model catalog to `model_definitions.go`**

Add this function to `internal/registry/model_definitions.go`, near
`GetMistralModels()` (confirmed at line 541):

```go
// GetAlysisModels returns the static Alysis Code Pro fallback catalog. It
// mirrors the models the reference CLI observed on the gateway; the live
// catalog is fetched at runtime from the gateway's OpenAI-shaped /models
// endpoint and this list is only used when that fetch fails or returns
// fewer entries.
func GetAlysisModels() []*ModelInfo {
	now := int64(1790860800) // 2026-10-01
	return []*ModelInfo{
		{
			ID:            "deepseek-v4-flash",
			Object:        "model",
			Created:       now,
			OwnedBy:       "alysis",
			Type:          "alysis",
			DisplayName:   "DeepSeek V4 Flash",
			Description:   "Alysis Code Pro flagship default model",
			ContextLength: 128000,
		},
		{ID: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", OwnedBy: "alysis", Type: "alysis", Object: "model", Created: now},
		{ID: "deepseek-v4-flash-vision-exp", DisplayName: "DeepSeek V4 Flash Vision (Exp)", OwnedBy: "alysis", Type: "alysis", Object: "model", Created: now},
	}
}
```

- [ ] **Step 2: Write the failing executor unit tests**

```go
// internal/runtime/executor/alysis_executor_test.go
package executor

import (
	"testing"

	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
)

func TestAlysisCredentials_FromMetadata(t *testing.T) {
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_abc"}}
	if got := alysisCredentials(auth); got != "slk_abc" {
		t.Errorf("got %q, want %q", got, "slk_abc")
	}
}

func TestAlysisCredentials_FallsBackToAttributes(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"gatewayKey": "slk_fallback"}}
	if got := alysisCredentials(auth); got != "slk_fallback" {
		t.Errorf("got %q, want %q", got, "slk_fallback")
	}
}

func TestAlysisCredentials_EmptyWhenMissing(t *testing.T) {
	if got := alysisCredentials(nil); got != "" {
		t.Errorf("got %q, want empty string for nil auth", got)
	}
	if got := alysisCredentials(&cliproxyauth.Auth{}); got != "" {
		t.Errorf("got %q, want empty string for auth with no credential", got)
	}
}

func TestAlysisChatCompletionsURL(t *testing.T) {
	e := NewAlysisExecutor(nil)
	got := e.chatCompletionsURL()
	want := "https://vzigujbcjjmpntxhmyvr.supabase.co/functions/v1/llm/v1/chat/completions"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/runtime/executor/... -run TestAlysis -v`
Expected: FAIL — `undefined: alysisCredentials` / `undefined:
NewAlysisExecutor`.

- [ ] **Step 4: Implement the executor**

```go
// internal/runtime/executor/alysis_executor.go
package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/registry"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/runtime/executor/helps"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/thinking"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/util"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
	sdktranslator "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// AlysisExecutor forwards OpenAI-compatible requests to the Alysis Code Pro
// hosted gateway, which meters subscription credits server-side.
type AlysisExecutor struct {
	cfg *config.Config
}

var _ cliproxyauth.ProviderExecutor = (*AlysisExecutor)(nil)

// NewAlysisExecutor creates a new Alysis executor instance.
func NewAlysisExecutor(cfg *config.Config) *AlysisExecutor {
	return &AlysisExecutor{cfg: cfg}
}

func (e *AlysisExecutor) Identifier() string { return "alysis" }

// alysisGatewayBase is overridable in tests.
var alysisGatewayBase = alysis.SupabaseURL + alysis.GatewayPathPrefix

func (e *AlysisExecutor) chatCompletionsURL() string {
	return alysisGatewayBase + "/chat/completions"
}

// HttpRequest injects the Alysis gateway key into the supplied request and executes it.
func (e *AlysisExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("alysis executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if key := alysisCredentials(auth); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Execute handles non-streaming chat completions against Alysis.
func (e *AlysisExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	apiKey := alysisCredentials(auth)
	if apiKey == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "alysis: missing gateway key"}
		return
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), false)

	url := e.chatCompletionsURL()
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if errReq != nil {
		return resp, errReq
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "application/json")
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, errDo := httpClient.Do(httpReq)
	if errDo != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errDo)
		return resp, errDo
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("alysis executor: close response body error: %v", errClose)
		}
	}()

	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}
	body, errRead := io.ReadAll(httpResp.Body)
	if errRead != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errRead)
		return resp, errRead
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, body)
	reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
	reporter.EnsurePublished(ctx)

	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, body, &param)
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream handles streaming chat completions against Alysis.
func (e *AlysisExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	apiKey := alysisCredentials(auth)
	if apiKey == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "alysis: missing gateway key"}
		return nil, err
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), true)

	url := e.chatCompletionsURL()
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if errReq != nil {
		return nil, errReq
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, errDo := httpClient.Do(httpReq)
	if errDo != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errDo)
		return nil, errDo
	}

	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("alysis executor: close response body error: %v", errClose)
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("alysis executor: close response body error: %v", errClose)
			}
		}()

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800)
		var param any
		for scanner.Scan() {
			line := scanner.Bytes()
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				continue
			}
			helps.AppendAPIResponseChunk(ctx, e.cfg, trimmed)
			if detail, ok := helps.ParseOpenAIStreamUsage(trimmed); ok {
				reporter.Publish(ctx, detail)
			}
			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, bytes.Clone(trimmed), &param)
			for i := range chunks {
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
				case <-ctx.Done():
					return
				}
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			helps.RecordAPIResponseError(ctx, e.cfg, errScan)
			reporter.PublishFailure(ctx, errScan)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: errScan}:
			case <-ctx.Done():
			}
			return
		}
		reporter.EnsurePublished(ctx)
	}()

	return &cliproxyexecutor.StreamResult{
		Headers: httpResp.Header.Clone(),
		Chunks:  out,
	}, nil
}

// Refresh validates the stored gateway key. The key is long-lived and has no
// server-side refresh endpoint, so the auth is returned unchanged.
func (e *AlysisExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("missing auth")
	}
	return auth, nil
}

// CountTokens is not supported by the Alysis gateway.
func (e *AlysisExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: "alysis: count tokens not supported"}
}

// alysisCredentials extracts the gateway key from the auth record.
func alysisCredentials(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Metadata != nil {
		if key, ok := auth.Metadata["gatewayKey"].(string); ok && key != "" {
			return key
		}
	}
	if auth.Attributes != nil {
		if key := auth.Attributes["gatewayKey"]; key != "" {
			return key
		}
	}
	return ""
}

// FetchAlysisModels fetches the live gateway model catalog (OpenAI-shaped
// {"data":[{"id":...}]}). Failures fall back to the static catalog.
func FetchAlysisModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	httpClient := helps.NewProxyAwareHTTPClient(ctx, cfg, auth, 0)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, alysisGatewayBase+"/models", nil)
	if err != nil {
		log.Warnf("alysis: failed to create model fetch request: %v", err)
		return registry.GetAlysisModels()
	}
	if key := alysisCredentials(auth); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Warnf("alysis: using static models (API fetch failed: %v)", err)
		return registry.GetAlysisModels()
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Warnf("alysis: failed to read models response: %v", err)
		return registry.GetAlysisModels()
	}
	if resp.StatusCode != http.StatusOK {
		log.Warnf("alysis: fetch models failed: status %d, body: %s", resp.StatusCode, string(body))
		return registry.GetAlysisModels()
	}

	result := gjson.GetBytes(body, "data")
	if !result.Exists() {
		log.Warnf("alysis: invalid models response format (expected data field)")
		return registry.GetAlysisModels()
	}

	now := time.Now().Unix()
	seen := make(map[string]struct{})
	merged := make([]*registry.ModelInfo, 0, 8)
	for _, model := range registry.GetAlysisModels() {
		if model == nil || model.ID == "" {
			continue
		}
		seen[model.ID] = struct{}{}
		merged = append(merged, model)
	}
	result.ForEach(func(_, value gjson.Result) bool {
		id := strings.TrimSpace(value.Get("id").String())
		if id == "" {
			return true
		}
		if _, exists := seen[id]; exists {
			return true
		}
		seen[id] = struct{}{}
		displayName := strings.TrimSpace(value.Get("name").String())
		if displayName == "" {
			displayName = id
		}
		merged = append(merged, &registry.ModelInfo{
			ID:          id,
			DisplayName: displayName,
			OwnedBy:     "alysis",
			Type:        "alysis",
			Object:      "model",
			Created:     now,
		})
		return true
	})

	if len(merged) == 0 {
		return registry.GetAlysisModels()
	}
	return merged
}
```

- [ ] **Step 5: Run the unit tests to verify they pass**

Run: `go test ./internal/runtime/executor/... -run TestAlysis -v`
Expected: PASS — all 4 unit tests green.

- [ ] **Step 6: Write the httptest integration tests**

```go
// internal/runtime/executor/alysis_executor_integration_test.go
package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	_ "github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
	sdktranslator "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"
)

func TestAlysisExecutor_Execute_Success(t *testing.T) {
	var sawAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization")
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"deepseek-v4-flash","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	exec := NewAlysisExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_test_token"}}
	req := cliproxyexecutor.Request{
		Model:   "deepseek-v4-flash",
		Payload: []byte(`{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"ping"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	resp, err := exec.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawAuthHeader != "Bearer slk_test_token" {
		t.Errorf("expected Authorization %q, got %q", "Bearer slk_test_token", sawAuthHeader)
	}
	if !strings.Contains(string(resp.Payload), "pong") {
		t.Errorf("expected response to contain %q, got: %s", "pong", resp.Payload)
	}
}

func TestAlysisExecutor_Execute_MissingGatewayKey(t *testing.T) {
	exec := NewAlysisExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{}}
	req := cliproxyexecutor.Request{Model: "deepseek-v4-flash", Payload: []byte(`{"model":"deepseek-v4-flash","messages":[]}`)}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	_, err := exec.Execute(context.Background(), auth, req, opts)
	if err == nil {
		t.Fatal("expected an error when the gateway key is missing")
	}
	se, ok := err.(statusErr)
	if !ok {
		t.Fatalf("expected statusErr, got %T: %v", err, err)
	}
	if se.StatusCode() != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", se.StatusCode())
	}
}

func TestAlysisExecutor_Execute_UpstreamErrorSurfaced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	exec := NewAlysisExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_test_token"}}
	req := cliproxyexecutor.Request{Model: "deepseek-v4-flash", Payload: []byte(`{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"ping"}]}`)}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	_, err := exec.Execute(context.Background(), auth, req, opts)
	if err == nil {
		t.Fatal("expected the upstream 429 to surface as an error")
	}
	se, ok := err.(statusErr)
	if !ok {
		t.Fatalf("expected statusErr, got %T: %v", err, err)
	}
	if se.StatusCode() != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", se.StatusCode())
	}
	if !strings.Contains(se.Error(), "rate limit exceeded") {
		t.Errorf("expected the real upstream message to be preserved, got: %s", se.Error())
	}
}

func TestFetchAlysisModels_FallsBackOnFetchFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_test_token"}}
	models := FetchAlysisModels(context.Background(), auth, &config.Config{})
	if len(models) != 3 {
		t.Fatalf("expected the 3-entry static fallback catalog, got %d entries", len(models))
	}
}

func TestFetchAlysisModels_MergesLiveCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-v4-flash"},{"id":"deepseek-v5-preview","name":"DeepSeek V5 Preview"}]}`))
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_test_token"}}
	models := FetchAlysisModels(context.Background(), auth, &config.Config{})

	var sawNewModel, sawStaticModel bool
	for _, m := range models {
		if m.ID == "deepseek-v5-preview" {
			sawNewModel = true
		}
		if m.ID == "deepseek-v4-pro" {
			sawStaticModel = true // from the static catalog, not in the live response
		}
	}
	if !sawNewModel {
		t.Error("expected the live-only model to be present")
	}
	if !sawStaticModel {
		t.Error("expected a static-catalog-only model to still be present (merge, not replace)")
	}
}

var _ = alysis.ProductSiteURL // keep the alysis import used if trimmed during edits
```

(As with earlier providers in this project: the trailing `var _ =` line
is only a safety net — if `alysis.ProductSiteURL` ends up genuinely
unused after you finish, delete that line and the `alysis` import
together; `gofmt`/`go vet` will confirm either way.)

- [ ] **Step 7: Run the integration tests**

Run: `go test ./internal/runtime/executor/... -run TestAlysis -v`
Expected: PASS — all 4 unit tests plus 5 integration tests green.

- [ ] **Step 8: gofmt, build, full package test, commit**

```bash
gofmt -w internal/runtime/executor/alysis_executor.go internal/runtime/executor/alysis_executor_test.go internal/runtime/executor/alysis_executor_integration_test.go internal/registry/model_definitions.go
go build ./... && echo "build OK"
go test ./internal/runtime/executor/... ./internal/registry/... -v 2>&1 | tail -40
git add internal/runtime/executor/alysis_executor*.go internal/registry/model_definitions.go
git commit -m "feat: add AlysisExecutor and static model catalog"
```

---

### Task 5: Management-API device-flow endpoint and TUI entry

**Files:**
- Modify: `internal/api/handlers/management/auth_files_provider_oauth.go`
- Modify: `cmd/server/main.go` (management route registration — separate
  from Task 3's CLI flag section)
- Modify: `internal/tui/oauth_tab.go`

**Interfaces:**
- Consumes: Task 1's `alysis` package (`alysis.NewAuth`,
  `alysis.TokenStorage`, `alysis.CredentialFileName`), this repo's
  existing `RegisterOAuthSession`, `IsOAuthSessionPending`,
  `SetOAuthSessionError`, `oauthSessionErrorWithCause`,
  `CompleteOAuthSession`, `watchOAuthSessionCancel`,
  `guardOAuthSessionPendingForSave`, `PopulateAuthContext`, and
  `(h *Handler) saveTokenRecord(ctx, record) (string, error)` — all
  confirmed present in `internal/api/handlers/management/` by reading
  `RequestXAIToken`'s full implementation as the template.
- Produces: `(h *Handler) RequestAlysisToken(c *gin.Context)`, the route
  `GET /v0/management/alysis-auth-url`, and a new `oauthProviders` entry.

- [ ] **Step 1: Implement the management-API handler**

Add this function to `internal/api/handlers/management/auth_files_provider_oauth.go`,
next to `RequestXAIToken`:

```go
// RequestAlysisToken implements GET /v0/management/alysis-auth-url.
// Alysis uses an RFC 8628-style device flow (no local callback): POST
// /functions/v1/device-code then poll /functions/v1/device-token until the
// user approves the code on https://alysiscode.com/activate.
func (h *Handler) RequestAlysisToken(c *gin.Context) {
	ctx := context.Background()
	ctx = PopulateAuthContext(ctx, c)

	fmt.Println("Initializing Alysis authentication...")

	state := fmt.Sprintf("alysis-%d", time.Now().UnixNano())
	authSvc := alysis.NewAuth()

	grant, errStart := authSvc.InitiateDeviceFlow(ctx)
	if errStart != nil {
		log.Errorf("Failed to start Alysis device flow: %v", errStart)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start device authorization flow"})
		return
	}
	authURL := strings.TrimSpace(grant.VerificationURLComplete)
	if authURL == "" {
		authURL = strings.TrimSpace(grant.VerificationURL)
	}
	if authURL == "" {
		authURL = alysis.ProductSiteURL + "/activate"
	}

	RegisterOAuthSession(state, "alysis")

	go func() {
		pollCtx, cancelPoll := context.WithCancel(ctx)
		defer cancelPoll()
		go watchOAuthSessionCancel(pollCtx, cancelPoll, state, "alysis")

		fmt.Println("Waiting for Alysis authentication...")
		status, errWait := authSvc.PollForToken(pollCtx, grant.DeviceCode)
		if errWait != nil {
			if !IsOAuthSessionPending(state, "alysis") {
				return
			}
			log.Errorf("Alysis authentication failed: %v", errWait)
			SetOAuthSessionError(state, oauthSessionErrorWithCause("Authentication failed", errWait))
			return
		}
		if !IsOAuthSessionPending(state, "alysis") {
			return
		}

		ts := &alysis.TokenStorage{
			Key:  status.Key,
			Type: "alysis",
		}
		fileName := alysis.CredentialFileName("")
		label := "Alysis Code Pro"

		metadata := map[string]any{
			"type":       "alysis",
			"gatewayKey": ts.Key,
			"email":      "",
			"auth_kind":  "oauth",
		}

		record := &coreauth.Auth{
			ID:       fileName,
			Provider: "alysis",
			FileName: fileName,
			Label:    label,
			Storage:  ts,
			Metadata: metadata,
			Attributes: map[string]string{
				"auth_kind": "oauth",
			},
		}
		if errGuard := guardOAuthSessionPendingForSave(state, "alysis"); errGuard != nil {
			return
		}
		savedPath, errSave := h.saveTokenRecord(ctx, record)
		if errSave != nil {
			log.Errorf("Failed to save Alysis token to file: %v", errSave)
			SetOAuthSessionError(state, "Failed to save token to file")
			return
		}

		CompleteOAuthSession(state)
		fmt.Printf("Authentication successful! Token saved to %s\n", savedPath)
		fmt.Println("You can now use Alysis Code Pro through this CLI")
	}()

	response := gin.H{"status": "ok", "url": authURL, "state": state, "flow": "device"}
	if userCode := strings.TrimSpace(grant.UserCode); userCode != "" {
		response["user_code"] = userCode
	}
	if grant.ExpiresIn > 0 {
		response["expires_in"] = grant.ExpiresIn
	}
	c.JSON(200, response)
}
```

Add the import `"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"`
(unaliased) to this file's import block, next to the existing unaliased
`"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/kimi"` import
(confirmed at line 20) — this file aliases only `meta`→`metaauth` and
`xai`→`xaiauth` (lines 21-22), while `antigravity`/`claude`/`codex`/`kimi`
import unaliased; `alysis` has no name collision risk in this file, so
follow the unaliased convention and reference it as `alysis.NewAuth()`
etc. exactly as written in the handler code above.

- [ ] **Step 2: Register the management route**

**File:** `cmd/server/main.go` — add next to the existing
`mgmt.GET("/kilo-auth-url", ...)` / similar device-flow routes (grep
`alysis-auth-url` is absent today; grep `kilo-auth-url` or `xai-auth-url`
to find the right block first):

```go
mgmt.GET("/alysis-auth-url", s.mgmt.RequestAlysisToken)
```

- [ ] **Step 3: Add the TUI entry**

**File:** `internal/tui/oauth_tab.go` — add to the `oauthProviders` slice,
after the `"Meta"` entry:

```go
{"Alysis Code Pro", "alysis-auth-url", "🟨", true},
```

(`🟨` is confirmed unused by the current 7-entry list — read in full
during planning: 🟧 🟩 🟪 🟫 🟫 ⬛ 🔵 — so no collision.)

- [ ] **Step 4: Build, verify, commit**

```bash
gofmt -w internal/api/handlers/management/auth_files_provider_oauth.go cmd/server/main.go internal/tui/oauth_tab.go
go build ./... && echo "build OK"
go vet ./internal/api/handlers/management/... ./internal/tui/... && echo "vet OK"
go test ./internal/api/handlers/management/... ./internal/tui/... 2>&1 | tail -20
git add internal/api/handlers/management/auth_files_provider_oauth.go cmd/server/main.go internal/tui/oauth_tab.go
git commit -m "feat: add Alysis management-API device-flow endpoint and TUI entry"
```

---

### Task 6: Wiring and final verification

**Files:**
- Modify: `sdk/cliproxy/service_executors.go`
- Modify: `sdk/cliproxy/service_models.go`

**Interfaces:**
- Consumes: `executor.NewAlysisExecutor`, `executor.FetchAlysisModels`
  (Task 4) — the exact `case "devin":` shape in both files is the
  template, confirmed by reading both files directly.

- [ ] **Step 1: Register the executor**

In `sdk/cliproxy/service_executors.go`, add next to the existing
`case "devin": s.coreManager.RegisterExecutor(executor.NewDevinExecutor(cfg))`:

```go
case "alysis":
	s.coreManager.RegisterExecutor(executor.NewAlysisExecutor(cfg))
```

Then add `"alysis"` to `baselineExecutorAuths()`'s provider list
(confirmed at line 214, next to `"devin"`).

- [ ] **Step 2: Wire model listing**

In `sdk/cliproxy/service_models.go`, add a sibling case right after the
`case "devin":` block (confirmed shape at line 167-169):

```go
case "alysis":
	models = executor.FetchAlysisModels(ctx, a, s.cfg)
	models = applyExcludedModels(models, excluded)
```

Check the exact variable names in scope at that point in the file (the
`devin` case uses `registry.GetDevinModels()` with no arguments, but
`FetchAlysisModels` needs `ctx`, the current auth record, and `cfg` —
confirm what these are named in the enclosing function signature before
writing the call; they are almost certainly `ctx`, `a` or `auth`, and
`s.cfg`, matching every other case's own variable usage in the same
function, but verify directly rather than assuming).

- [ ] **Step 3: Build, vet, full test suite**

```bash
go build -o /tmp/kaori-alysis-final ./cmd/server && rm /tmp/kaori-alysis-final && echo "build OK"
go vet ./... 2>&1 | tail -20
go test ./... 2>&1 | tee /tmp/alysis-test.log | tail -5
grep -c "^ok" /tmp/alysis-test.log
grep -n "FAIL" /tmp/alysis-test.log
```

Expected: build OK, vet clean, test log shows no `FAIL` lines, `ok` count
one higher than the pre-Alysis baseline (one new `internal/auth/alysis`
package, same count otherwise since no other package gained a
distinct testable unit).

- [ ] **Step 4: gofmt check across every file this plan touched**

```bash
gofmt -l internal/auth/alysis/ sdk/auth/alysis.go sdk/auth/alysis_test.go sdk/auth/refresh_registry.go internal/cmd/alysis_login.go internal/cmd/auth_manager.go sdk/cliproxy/service_auth.go cmd/server/main.go internal/runtime/executor/alysis_executor.go internal/runtime/executor/alysis_executor_test.go internal/runtime/executor/alysis_executor_integration_test.go internal/registry/model_definitions.go internal/api/handlers/management/auth_files_provider_oauth.go internal/tui/oauth_tab.go sdk/cliproxy/service_executors.go sdk/cliproxy/service_models.go
```

Expected: no output. If anything is listed, `gofmt -w` it and re-check.

- [ ] **Step 5: Commit the wiring**

```bash
git add sdk/cliproxy/service_executors.go sdk/cliproxy/service_models.go
git commit -m "feat: wire Alysis executor into executor registration and model listing"
```

- [ ] **Step 6: Mandatory manual live-verification gate (not automatable — do not skip, do not merge without it)**

This plan's automation ends here. Before this branch merges to `main`:

1. The user runs `--alysis-login` (or uses the management-API endpoint /
   TUI) with their own real Alysis Code Pro account, approving the
   device code at `alysiscode.com/activate`.
2. The user confirms a real chat-completion request through KAORI ROUTER
   reaches the live Alysis gateway and gets a real DeepSeek-backed
   response (both streaming and non-streaming, if practical).
3. Only after the user explicitly confirms this live test succeeded does
   this branch get merged to `main` — this is a human decision gate, not
   a step any agent should perform or claim as complete on the user's
   behalf.
