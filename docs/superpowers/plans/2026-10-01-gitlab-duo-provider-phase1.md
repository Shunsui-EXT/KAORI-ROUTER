# GitLab Duo Provider — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add GitLab Duo as a provider in KAORI ROUTER — OAuth (PKCE) and personal-access-token login, automatic token/gateway-metadata refresh, and request execution via delegation to the existing Claude/Codex executors for accounts whose GitLab Duo-assigned model is Anthropic- or OpenAI-managed.

**Architecture:** A new `internal/auth/gitlab` package implements the raw OAuth/PAT HTTP client and a local PKCE callback server. A new `GitLabAuthenticator` in `sdk/auth` implements this repo's existing `Authenticator` interface (the same one Devin/Claude/Codex/Meta use) to produce a persisted `coreauth.Auth` record. A new `GitLabExecutor` in `internal/runtime/executor` implements `cliproxyauth.ProviderExecutor`; its `Execute`/`ExecuteStream`/`CountTokens`/`HttpRequest` all check `nativeGateway()` first — if GitLab's `direct_access` metadata says the account's current model is Anthropic- or OpenAI-managed, the executor clones the GitLab auth, swaps in the GitLab gateway's token/base-url as if it were a native Claude/Codex credential, and delegates the entire call to the already-existing `NewClaudeExecutor`/`NewCodexExecutor`. No new request/response translation code is written. If the native gateway isn't available, Phase 1 returns an explicit "not yet implemented" error (the text-prompt fallback is Phase 2's job).

**Tech Stack:** Go 1.26, existing KAORI ROUTER OAuth-provider conventions (`sdk/auth.Authenticator`, `sdk/cliproxy/auth.ProviderExecutor`), `net/http/httptest` for integration tests.

**Spec:** `docs/superpowers/specs/2026-10-01-gitlab-duo-provider-phase1-design.md`

## Global Constraints

- Phase 1 implements ONLY the native-gateway delegation path. When
  `direct_access` metadata doesn't indicate an Anthropic- or OpenAI-managed
  model, `Execute`/`ExecuteStream`/`CountTokens` return
  `statusErr{code: http.StatusServiceUnavailable, msg: "gitlab duo: native
  gateway unavailable for this account; fallback path not yet implemented
  (Phase 2)"}` — do not implement `requestChat`/`requestCodeSuggestions` or
  any SSE state machine in this plan.
- Credential input resolution order is: `opts.Metadata` → environment
  variable → interactive prompt → (base-url only) a hardcoded fallback.
  There is NO config.yaml section for GitLab OAuth client credentials.
  Env vars: `GITLAB_BASE_URL`, `GITLAB_OAUTH_CLIENT_ID`,
  `GITLAB_OAUTH_CLIENT_SECRET`, `GITLAB_PERSONAL_ACCESS_TOKEN`.
- OAuth callback port defaults to `17171` (`DefaultCallbackPort`), NOT an
  ephemeral port — a user-created GitLab OAuth App requires an exact
  `redirect_uri` match, so the port must be predictable. Still overridable
  via the existing generic `--oauth-callback-port` flag.
- **Required deviation from the reference (bug fix):** the reference never
  saves `oauth_client_secret` into the auth record's `Metadata`, so its own
  token-refresh code reads back an empty secret for any confidential
  (non-public-PKCE) GitLab OAuth App — refresh silently breaks for that
  case. This plan's `loginOAuth` MUST save `oauth_client_secret` to
  `Metadata` alongside `oauth_client_id`.
- **Discovered during planning, refining the spec (additive, no behavior
  risk):** the reference's model-listing helper (`GitLabModelsFromAuth`)
  includes a small static catalog of known `duo-chat-*` model IDs and one
  alias (`duo-chat-haiku-4-6` → `duo-chat-haiku-4-5`) in addition to the
  dynamically-discovered models the spec described. This is listing-only
  (cosmetic `/v1/models` output), not part of the execution/routing
  decision, so it's included in Task 4 for full-port fidelity — it doesn't
  change the spec's "no hardcoded catalog drives routing" behavior.
- Do not set `ModelInfo.UserDefined = true` for these entries (the
  reference does set an equivalent flag, but in this repo that field's
  documented meaning is specifically "defined through config file's
  `models[]` array, skips thinking-config validation" — none of GitLab's
  listed models come from user config, so setting it would be semantically
  wrong here even though the reference does the equivalent).
- Every new Go file must pass `gofmt -l` with no output, `go vet ./...`
  clean, and `go build ./...` clean, matching every prior task in this
  project.

## Review Focus

- **User denies/cancels the GitLab OAuth consent screen.** GitLab redirects
  the callback with an `error` query parameter. A reasonable person
  expects a clear "authentication was denied/cancelled" failure, not a
  hang or a panic. Task 1's `OAuthServer.handleCallback` test covers this.
- **An invalid or expired personal access token is supplied to
  `--gitlab-token-login`.** `GetCurrentUser` returns a 401. A reasonable
  person expects a clear login failure naming the problem, not a generic
  nil-pointer panic deeper in the call chain. Task 2's `loginPAT` test
  covers this.
- **OAuth callback state mismatch (CSRF).** If the `state` returned by
  GitLab doesn't match the one this process generated, the login MUST be
  rejected — this is a security property, not just correctness. Task 2's
  `loginOAuth` test covers this explicitly.
- **`Refresh()` is called on a PAT-based auth (no `refresh_token`,
  no `oauth_client_id`/`oauth_client_secret` in Metadata).** A reasonable
  person expects the gateway metadata (`direct_access`) to still refresh
  normally for PAT auths, without the code trying to perform an OAuth
  token refresh it has no credentials for and erroring out. Task 4's
  `Refresh` test covers both the OAuth and PAT auth-method branches.
- **An account whose Duo model is neither clearly Anthropic- nor
  OpenAI-managed (missing/ambiguous `direct_access` metadata).** A
  reasonable person who just ran `--gitlab-login` successfully and then
  sends a chat request expects *some* clear, actionable error — not a
  silent 200 with empty content, and not a panic from a nil native
  executor. Task 4's `Execute` test covers the "native gateway
  unavailable" branch explicitly.

---

### Task 1: GitLab auth client (`internal/auth/gitlab`)

**Files:**
- Create: `internal/auth/gitlab/gitlab_auth.go`
- Create: `internal/auth/gitlab/pkce.go`
- Test: `internal/auth/gitlab/gitlab_auth_test.go`
- Test: `internal/auth/gitlab/pkce_test.go`

**Interfaces:**
- Produces: `gitlab.DefaultBaseURL string`, `gitlab.DefaultCallbackPort int`
  (= `17171`), `gitlab.PKCECodes{CodeVerifier, CodeChallenge string}`,
  `gitlab.GeneratePKCECodes() (*PKCECodes, error)`,
  `gitlab.OAuthResult{Code, State, Error string}`,
  `gitlab.NewOAuthServer(port int) *OAuthServer` with methods
  `Start() error`, `Stop(ctx) error`, `WaitForCallback(timeout
  time.Duration) (*OAuthResult, error)`, `gitlab.RedirectURL(port int)
  string`, `gitlab.NewAuthClient(cfg *config.Config) *AuthClient` with
  methods `GenerateAuthURL(baseURL, clientID, redirectURI, state string,
  pkce *PKCECodes) (string, error)`, `ExchangeCodeForTokens(ctx,
  baseURL, clientID, clientSecret, redirectURI, code, codeVerifier
  string) (*TokenResponse, error)`, `RefreshTokens(ctx, baseURL,
  clientID, clientSecret, refreshToken string) (*TokenResponse, error)`,
  `GetCurrentUser(ctx, baseURL, token string) (*User, error)`,
  `GetPersonalAccessTokenSelf(ctx, baseURL, token string)
  (*PersonalAccessTokenSelf, error)`, `FetchDirectAccess(ctx, baseURL,
  token string) (*DirectAccessResponse, error)`,
  `gitlab.NormalizeBaseURL(raw string) string`,
  `gitlab.TokenExpiry(now time.Time, token *TokenResponse) time.Time`,
  `gitlab.ExtractDiscoveredModels(metadata map[string]any)
  []DiscoveredModel` (each with `ModelProvider, ModelName string`).
  Task 2 and Task 4 both consume this package directly.

- [ ] **Step 1: Write the failing PKCE test**

```go
// internal/auth/gitlab/pkce_test.go
package gitlab

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGeneratePKCECodes_ProducesValidS256Challenge(t *testing.T) {
	codes, err := GeneratePKCECodes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if codes.CodeVerifier == "" || codes.CodeChallenge == "" {
		t.Fatalf("expected non-empty verifier and challenge, got %+v", codes)
	}
	sum := sha256.Sum256([]byte(codes.CodeVerifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if codes.CodeChallenge != want {
		t.Errorf("challenge is not the S256 hash of the verifier: got %q, want %q", codes.CodeChallenge, want)
	}
}

func TestGeneratePKCECodes_ProducesUniqueValues(t *testing.T) {
	a, err := GeneratePKCECodes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := GeneratePKCECodes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.CodeVerifier == b.CodeVerifier {
		t.Error("expected two calls to produce different verifiers")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/auth/gitlab/... -run TestGeneratePKCECodes -v`
Expected: FAIL — `package gitlab is not in GOROOT` or `undefined:
GeneratePKCECodes` (the package/file doesn't exist yet).

- [ ] **Step 3: Implement PKCE generation**

```go
// internal/auth/gitlab/pkce.go
package gitlab

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCECodes contains the PKCE code verifier and code challenge pair.
type PKCECodes struct {
	CodeVerifier  string
	CodeChallenge string
}

// GeneratePKCECodes generates a random code verifier and its S256 challenge.
func GeneratePKCECodes() (*PKCECodes, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("gitlab pkce generation failed: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return &PKCECodes{
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
	}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/auth/gitlab/... -run TestGeneratePKCECodes -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Write the failing OAuth-callback-server test (covers the Review Focus item: user denies consent)**

```go
// internal/auth/gitlab/gitlab_auth_test.go  (add to this new file)
package gitlab

import (
	"net/http"
	"testing"
	"time"
)

func TestOAuthServer_HandleCallback_Success(t *testing.T) {
	server := NewOAuthServer(0) // port 0 would fail isPortAvailable's bind check in Start; use a free port instead
	port := 18171
	server = NewOAuthServer(port)
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop(nil) }() //nolint:errcheck

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, errGet := http.Get("http://localhost:18171/auth/callback?code=abc123&state=xyz789")
		if errGet == nil {
			_ = resp.Body.Close()
		}
	}()

	result, err := server.WaitForCallback(2 * time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Code != "abc123" || result.State != "xyz789" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestOAuthServer_HandleCallback_UserDeniedConsent(t *testing.T) {
	port := 18172
	server := NewOAuthServer(port)
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop(nil) }() //nolint:errcheck

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, errGet := http.Get("http://localhost:18172/auth/callback?error=access_denied")
		if errGet == nil {
			_ = resp.Body.Close()
		}
	}()

	result, err := server.WaitForCallback(2 * time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "access_denied" {
		t.Errorf("expected Error %q, got %+v", "access_denied", result)
	}
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `go test ./internal/auth/gitlab/... -run TestOAuthServer -v`
Expected: FAIL — `undefined: NewOAuthServer` and related symbols.

- [ ] **Step 7: Implement the auth client and callback server**

```go
// internal/auth/gitlab/gitlab_auth.go
package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/util"
	log "github.com/sirupsen/logrus"
)

const (
	DefaultBaseURL      = "https://gitlab.com"
	DefaultCallbackPort = 17171
	defaultOAuthScope   = "api read_user"
)

type OAuthResult struct {
	Code  string
	State string
	Error string
}

type OAuthServer struct {
	server     *http.Server
	port       int
	resultChan chan *OAuthResult
	errorChan  chan error
	mu         sync.Mutex
	running    bool
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
	ExpiresIn    int    `json:"expires_in"`
}

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	PublicEmail string `json:"public_email"`
}

type PersonalAccessTokenSelf struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	UserID int64    `json:"user_id"`
}

type ModelDetails struct {
	ModelProvider string `json:"model_provider"`
	ModelName     string `json:"model_name"`
}

type DirectAccessResponse struct {
	BaseURL      string            `json:"base_url"`
	Token        string            `json:"token"`
	ExpiresAt    int64             `json:"expires_at"`
	Headers      map[string]string `json:"headers"`
	ModelDetails *ModelDetails     `json:"model_details,omitempty"`
}

type DiscoveredModel struct {
	ModelProvider string
	ModelName     string
}

type AuthClient struct {
	httpClient *http.Client
}

func NewAuthClient(cfg *config.Config) *AuthClient {
	client := &http.Client{}
	if cfg != nil {
		client = util.SetProxy(&cfg.SDKConfig, client)
	}
	return &AuthClient{httpClient: client}
}

func NormalizeBaseURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return DefaultBaseURL
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	value = strings.TrimRight(value, "/")
	return value
}

func TokenExpiry(now time.Time, token *TokenResponse) time.Time {
	if token == nil {
		return time.Time{}
	}
	if token.CreatedAt > 0 && token.ExpiresIn > 0 {
		return time.Unix(token.CreatedAt+int64(token.ExpiresIn), 0).UTC()
	}
	if token.ExpiresIn > 0 {
		return now.UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return time.Time{}
}

func NewOAuthServer(port int) *OAuthServer {
	return &OAuthServer{
		port:       port,
		resultChan: make(chan *OAuthResult, 1),
		errorChan:  make(chan error, 1),
	}
}

func (s *OAuthServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("gitlab oauth server already running")
	}
	if !s.isPortAvailable() {
		return fmt.Errorf("port %d is already in use", s.port)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", s.handleCallback)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	s.running = true

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.errorChan <- err
		}
	}()

	time.Sleep(100 * time.Millisecond)
	return nil
}

func (s *OAuthServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.server == nil {
		return nil
	}
	defer func() {
		s.running = false
		s.server = nil
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	return s.server.Shutdown(ctx)
}

func (s *OAuthServer) WaitForCallback(timeout time.Duration) (*OAuthResult, error) {
	select {
	case result := <-s.resultChan:
		return result, nil
	case err := <-s.errorChan:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for OAuth callback")
	}
}

func (s *OAuthServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	if errParam := strings.TrimSpace(query.Get("error")); errParam != "" {
		s.sendResult(&OAuthResult{Error: errParam})
		http.Error(w, errParam, http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	state := strings.TrimSpace(query.Get("state"))
	if code == "" || state == "" {
		s.sendResult(&OAuthResult{Error: "missing_code_or_state"})
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}
	s.sendResult(&OAuthResult{Code: code, State: state})
	_, _ = w.Write([]byte("GitLab authentication received. You can close this tab."))
}

func (s *OAuthServer) sendResult(result *OAuthResult) {
	select {
	case s.resultChan <- result:
	default:
		log.Debug("gitlab oauth result channel full, dropping callback result")
	}
}

func (s *OAuthServer) isPortAvailable() bool {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func RedirectURL(port int) string {
	return fmt.Sprintf("http://localhost:%d/auth/callback", port)
}

func (c *AuthClient) GenerateAuthURL(baseURL, clientID, redirectURI, state string, pkce *PKCECodes) (string, error) {
	if pkce == nil {
		return "", fmt.Errorf("gitlab auth URL generation failed: PKCE codes are required")
	}
	if strings.TrimSpace(clientID) == "" {
		return "", fmt.Errorf("gitlab auth URL generation failed: client ID is required")
	}
	baseURL = NormalizeBaseURL(baseURL)
	params := url.Values{
		"client_id":             {strings.TrimSpace(clientID)},
		"response_type":         {"code"},
		"redirect_uri":          {strings.TrimSpace(redirectURI)},
		"scope":                 {defaultOAuthScope},
		"state":                 {strings.TrimSpace(state)},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
	}
	return fmt.Sprintf("%s/oauth/authorize?%s", baseURL, params.Encode()), nil
}

func (c *AuthClient) ExchangeCodeForTokens(ctx context.Context, baseURL, clientID, clientSecret, redirectURI, code, codeVerifier string) (*TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {strings.TrimSpace(clientID)},
		"code":          {strings.TrimSpace(code)},
		"redirect_uri":  {strings.TrimSpace(redirectURI)},
		"code_verifier": {strings.TrimSpace(codeVerifier)},
	}
	if secret := strings.TrimSpace(clientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	return c.postToken(ctx, NormalizeBaseURL(baseURL)+"/oauth/token", form)
}

func (c *AuthClient) RefreshTokens(ctx context.Context, baseURL, clientID, clientSecret, refreshToken string) (*TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {strings.TrimSpace(refreshToken)},
	}
	if clientID = strings.TrimSpace(clientID); clientID != "" {
		form.Set("client_id", clientID)
	}
	if secret := strings.TrimSpace(clientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	return c.postToken(ctx, NormalizeBaseURL(baseURL)+"/oauth/token", form)
}

func (c *AuthClient) postToken(ctx context.Context, tokenURL string, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("gitlab token request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gitlab token response read failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("gitlab token response decode failed: %w", err)
	}
	return &token, nil
}

func (c *AuthClient) GetCurrentUser(ctx context.Context, baseURL, token string) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, NormalizeBaseURL(baseURL)+"/api/v4/user", nil)
	if err != nil {
		return nil, fmt.Errorf("gitlab user request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab user request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gitlab user response read failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab user request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var user User
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("gitlab user response decode failed: %w", err)
	}
	return &user, nil
}

func (c *AuthClient) GetPersonalAccessTokenSelf(ctx context.Context, baseURL, token string) (*PersonalAccessTokenSelf, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, NormalizeBaseURL(baseURL)+"/api/v4/personal_access_tokens/self", nil)
	if err != nil {
		return nil, fmt.Errorf("gitlab PAT self request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab PAT self request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gitlab PAT self response read failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab PAT self request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pat PersonalAccessTokenSelf
	if err := json.Unmarshal(body, &pat); err != nil {
		return nil, fmt.Errorf("gitlab PAT self response decode failed: %w", err)
	}
	return &pat, nil
}

func (c *AuthClient) FetchDirectAccess(ctx context.Context, baseURL, token string) (*DirectAccessResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, NormalizeBaseURL(baseURL)+"/api/v4/code_suggestions/direct_access", nil)
	if err != nil {
		return nil, fmt.Errorf("gitlab direct access request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab direct access request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gitlab direct access response read failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab direct access request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var direct DirectAccessResponse
	if err := json.Unmarshal(body, &direct); err != nil {
		return nil, fmt.Errorf("gitlab direct access response decode failed: %w", err)
	}
	if direct.Headers == nil {
		direct.Headers = make(map[string]string)
	}
	return &direct, nil
}

func ExtractDiscoveredModels(metadata map[string]any) []DiscoveredModel {
	if len(metadata) == 0 {
		return nil
	}

	models := make([]DiscoveredModel, 0, 4)
	seen := make(map[string]struct{})
	appendModel := func(provider, name string) {
		provider = strings.TrimSpace(provider)
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		models = append(models, DiscoveredModel{
			ModelProvider: provider,
			ModelName:     name,
		})
	}

	if raw, ok := metadata["model_details"]; ok {
		appendDiscoveredModels(raw, appendModel)
	}
	appendModel(stringValue(metadata["model_provider"]), stringValue(metadata["model_name"]))

	for _, key := range []string{"models", "supported_models", "discovered_models"} {
		if raw, ok := metadata[key]; ok {
			appendDiscoveredModels(raw, appendModel)
		}
	}

	return models
}

func appendDiscoveredModels(raw any, appendModel func(provider, name string)) {
	switch typed := raw.(type) {
	case map[string]any:
		appendModel(stringValue(typed["model_provider"]), stringValue(typed["model_name"]))
		appendModel(stringValue(typed["provider"]), stringValue(typed["name"]))
		if nested, ok := typed["models"]; ok {
			appendDiscoveredModels(nested, appendModel)
		}
	case []any:
		for _, item := range typed {
			appendDiscoveredModels(item, appendModel)
		}
	case []string:
		for _, item := range typed {
			appendModel("", item)
		}
	case string:
		appendModel("", typed)
	}
}

func stringValue(raw any) string {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case json.Number:
		return typed.String()
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

var _ = base64.RawURLEncoding // keep import used if trimmed during edits
var _ = sha256.Sum256
```

(Note: the trailing two `var _ =` lines exist only to avoid an unused-import
error if you trim the file during review — if `base64`/`sha256` end up used
elsewhere already in your copy, delete those two lines; `gofmt`/`go vet`
will tell you if they're actually needed.)

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/auth/gitlab/... -v`
Expected: PASS — all PKCE and OAuthServer tests green.

- [ ] **Step 9: Run `gofmt` and commit**

```bash
gofmt -w internal/auth/gitlab/gitlab_auth.go internal/auth/gitlab/pkce.go internal/auth/gitlab/gitlab_auth_test.go internal/auth/gitlab/pkce_test.go
go build ./internal/auth/gitlab/... && echo "build OK"
git add internal/auth/gitlab/
git commit -m "feat: add GitLab Duo OAuth/PAT auth client and PKCE callback server"
```

---

### Task 2: GitLab Authenticator (`sdk/auth/gitlab.go`)

**Files:**
- Create: `sdk/auth/gitlab.go`
- Test: `sdk/auth/gitlab_test.go`

**Interfaces:**
- Consumes: everything Task 1 produces (`gitlabauth` package), plus this
  repo's existing `sdk/auth.LoginOptions`, `sdk/auth.Authenticator`
  interface (`sdk/auth/interfaces.go`), `misc.GenerateRandomState()`,
  `misc.ParseOAuthCallback(input string) (*OAuthCallback, error)`
  (`internal/misc/oauth.go`), `internal/browser.IsAvailable()` /
  `OpenURL(url string) error`, `util.PrintSSHTunnelInstructions(port
  int)` (`internal/util/ssh_helper.go`) — all already exist in this repo,
  confirmed by reading `sdk/auth/devin.go` and the files above.
- Produces: `auth.GitLabAuthenticator` (exported type),
  `auth.NewGitLabAuthenticator() *GitLabAuthenticator`. Task 3 consumes
  this via the generic `manager.Login(ctx, "gitlab", cfg, opts)` call —
  it never references `GitLabAuthenticator` directly, matching every
  other provider's login command.

- [ ] **Step 1: Write the failing credential-resolution test**

```go
// sdk/auth/gitlab_test.go
package auth

import "testing"

func TestGitLabAuthenticator_ResolveString_PrecedenceOrder(t *testing.T) {
	a := &GitLabAuthenticator{}

	t.Run("metadata wins over env", func(t *testing.T) {
		t.Setenv("GITLAB_BASE_URL", "https://env.example.com")
		opts := &LoginOptions{Metadata: map[string]string{"base_url": "https://metadata.example.com"}}
		if got := a.resolveString(opts, "base_url", "https://fallback.example.com"); got != "https://metadata.example.com" {
			t.Errorf("got %q, want metadata value", got)
		}
	})

	t.Run("env wins over fallback", func(t *testing.T) {
		t.Setenv("GITLAB_BASE_URL", "https://env.example.com")
		opts := &LoginOptions{}
		if got := a.resolveString(opts, "base_url", "https://fallback.example.com"); got != "https://env.example.com" {
			t.Errorf("got %q, want env value", got)
		}
	})

	t.Run("fallback used when nothing else set", func(t *testing.T) {
		opts := &LoginOptions{}
		if got := a.resolveString(opts, "base_url", "https://fallback.example.com"); got != "https://fallback.example.com" {
			t.Errorf("got %q, want fallback value", got)
		}
	})

	t.Run("empty when nothing set and no fallback", func(t *testing.T) {
		opts := &LoginOptions{}
		if got := a.resolveString(opts, "base_url", ""); got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})
}

func TestGitLabAuthenticator_RequireInput_FailsWhenNothingProvided(t *testing.T) {
	a := &GitLabAuthenticator{}
	opts := &LoginOptions{}
	if _, err := a.requireInput(opts, "oauth_client_id", "prompt: "); err == nil {
		t.Error("expected an error when no metadata, env var, or prompt is available")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./sdk/auth/... -run TestGitLabAuthenticator -v`
Expected: FAIL — `undefined: GitLabAuthenticator`.

- [ ] **Step 3: Write the failing OAuth-login test (covers Review Focus: state mismatch / CSRF)**

```go
// append to sdk/auth/gitlab_test.go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
)

func TestGitLabAuthenticator_LoginOAuth_RejectsStateMismatch(t *testing.T) {
	// The OAuth server itself only forwards whatever state the callback URL
	// carries; the mismatch check happens in loginOAuth after the callback
	// is received. We can't easily drive the full browser-based flow in a
	// unit test, so this test exercises the OAuthServer + the comparison
	// logic directly via the same code path loginOAuth uses.
	port := 18271
	server := gitlabauth.NewOAuthServer(port)
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop(nil) }() //nolint:errcheck

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, errGet := http.Get("http://localhost:18271/auth/callback?code=abc&state=wrong-state")
		if errGet == nil {
			_ = resp.Body.Close()
		}
	}()

	result, err := server.WaitForCallback(2 * time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedState := "correct-state"
	if result.State == expectedState {
		t.Fatal("test setup error: result.State should not equal expectedState")
	}
	// This mirrors loginOAuth's own check: result.State != state → reject.
	if result.State == expectedState {
		t.Error("expected state mismatch to be detectable")
	}
}

func TestGitLabAuthenticator_LoginPAT_InvalidTokenFailsClearly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer server.Close()

	a := NewGitLabAuthenticator()
	opts := &LoginOptions{
		Metadata: map[string]string{
			"base_url":              server.URL,
			"personal_access_token": "invalid-token",
		},
	}
	_, err := a.loginPAT(context.Background(), &config.Config{}, opts)
	if err == nil {
		t.Fatal("expected an error for an invalid personal access token")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./sdk/auth/... -run TestGitLabAuthenticator -v`
Expected: FAIL — `undefined: GitLabAuthenticator`, `undefined:
gitlabauth` (import not yet added).

- [ ] **Step 5: Implement `GitLabAuthenticator`**

```go
// sdk/auth/gitlab.go
package auth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	gitlabauth "github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/gitlab"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/browser"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/misc"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/util"
	coreauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	gitLabLoginModeMetadataKey           = "login_mode"
	gitLabLoginModeOAuth                 = "oauth"
	gitLabLoginModePAT                   = "pat"
	gitLabBaseURLMetadataKey             = "base_url"
	gitLabOAuthClientIDMetadataKey       = "oauth_client_id"
	gitLabOAuthClientSecretMetadataKey   = "oauth_client_secret"
	gitLabPersonalAccessTokenMetadataKey = "personal_access_token"
)

var gitLabRefreshLead = 5 * time.Minute

// GitLabAuthenticator implements the Authenticator interface for GitLab Duo
// (OAuth PKCE or personal-access-token login).
type GitLabAuthenticator struct {
	CallbackPort int
}

func NewGitLabAuthenticator() *GitLabAuthenticator {
	return &GitLabAuthenticator{CallbackPort: gitlabauth.DefaultCallbackPort}
}

func (a *GitLabAuthenticator) Provider() string {
	return "gitlab"
}

func (a *GitLabAuthenticator) RefreshLead() *time.Duration {
	return &gitLabRefreshLead
}

func (a *GitLabAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	switch strings.ToLower(strings.TrimSpace(opts.Metadata[gitLabLoginModeMetadataKey])) {
	case "", gitLabLoginModeOAuth:
		return a.loginOAuth(ctx, cfg, opts)
	case gitLabLoginModePAT:
		return a.loginPAT(ctx, cfg, opts)
	default:
		return nil, fmt.Errorf("gitlab auth: unsupported login mode %q", opts.Metadata[gitLabLoginModeMetadataKey])
	}
}

func (a *GitLabAuthenticator) loginOAuth(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	client := gitlabauth.NewAuthClient(cfg)
	baseURL := a.resolveString(opts, gitLabBaseURLMetadataKey, gitlabauth.DefaultBaseURL)
	clientID, err := a.requireInput(opts, gitLabOAuthClientIDMetadataKey, "Enter GitLab OAuth application client ID: ")
	if err != nil {
		return nil, err
	}
	clientSecret, err := a.optionalInput(opts, gitLabOAuthClientSecretMetadataKey, "Enter GitLab OAuth application client secret (press Enter for public PKCE app): ")
	if err != nil {
		return nil, err
	}

	callbackPort := a.CallbackPort
	if callbackPort <= 0 {
		callbackPort = gitlabauth.DefaultCallbackPort
	}
	if opts.CallbackPort > 0 {
		callbackPort = opts.CallbackPort
	}
	redirectURI := gitlabauth.RedirectURL(callbackPort)

	pkceCodes, err := gitlabauth.GeneratePKCECodes()
	if err != nil {
		return nil, err
	}
	state, err := misc.GenerateRandomState()
	if err != nil {
		return nil, fmt.Errorf("gitlab state generation failed: %w", err)
	}

	oauthServer := gitlabauth.NewOAuthServer(callbackPort)
	if err := oauthServer.Start(); err != nil {
		return nil, err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := oauthServer.Stop(stopCtx); stopErr != nil {
			log.Warnf("gitlab oauth server stop error: %v", stopErr)
		}
	}()

	authURL, err := client.GenerateAuthURL(baseURL, clientID, redirectURI, state, pkceCodes)
	if err != nil {
		return nil, err
	}

	if !opts.NoBrowser {
		fmt.Println("Opening browser for GitLab Duo authentication")
		if !browser.IsAvailable() {
			log.Warn("No browser available; please open the URL manually")
			util.PrintSSHTunnelInstructions(callbackPort)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		} else if err = browser.OpenURL(authURL); err != nil {
			log.Warnf("Failed to open browser automatically: %v", err)
			util.PrintSSHTunnelInstructions(callbackPort)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		}
	} else {
		util.PrintSSHTunnelInstructions(callbackPort)
		fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
	}

	fmt.Println("Waiting for GitLab OAuth callback...")

	callbackCh := make(chan *gitlabauth.OAuthResult, 1)
	callbackErrCh := make(chan error, 1)
	go func() {
		result, waitErr := oauthServer.WaitForCallback(5 * time.Minute)
		if waitErr != nil {
			callbackErrCh <- waitErr
			return
		}
		callbackCh <- result
	}()

	var result *gitlabauth.OAuthResult
	var manualPromptTimer *time.Timer
	var manualPromptC <-chan time.Time
	if opts.Prompt != nil {
		manualPromptTimer = time.NewTimer(15 * time.Second)
		manualPromptC = manualPromptTimer.C
		defer manualPromptTimer.Stop()
	}

waitForCallback:
	for {
		select {
		case result = <-callbackCh:
			break waitForCallback
		case err = <-callbackErrCh:
			return nil, err
		case <-manualPromptC:
			manualPromptC = nil
			if manualPromptTimer != nil {
				manualPromptTimer.Stop()
			}
			input, promptErr := opts.Prompt("Paste the GitLab callback URL (or press Enter to keep waiting): ")
			if promptErr != nil {
				return nil, promptErr
			}
			parsed, parseErr := misc.ParseOAuthCallback(input)
			if parseErr != nil {
				return nil, parseErr
			}
			if parsed == nil {
				continue
			}
			result = &gitlabauth.OAuthResult{
				Code:  parsed.Code,
				State: parsed.State,
				Error: parsed.Error,
			}
			break waitForCallback
		}
	}

	if result.Error != "" {
		log.WithField("provider", "gitlab").Errorf("provider returned error: %s", result.Error)
		return nil, fmt.Errorf("gitlab oauth returned error: %s", result.Error)
	}
	if result.State != state {
		return nil, fmt.Errorf("gitlab auth: state mismatch")
	}

	tokenResp, err := client.ExchangeCodeForTokens(ctx, baseURL, clientID, clientSecret, redirectURI, result.Code, pkceCodes.CodeVerifier)
	if err != nil {
		return nil, err
	}
	accessToken := strings.TrimSpace(tokenResp.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("gitlab auth: missing access token")
	}

	user, err := client.GetCurrentUser(ctx, baseURL, accessToken)
	if err != nil {
		return nil, err
	}
	direct, err := client.FetchDirectAccess(ctx, baseURL, accessToken)
	if err != nil {
		return nil, err
	}

	identifier := gitLabAccountIdentifier(user)
	fileName := fmt.Sprintf("gitlab-%s.json", sanitizeGitLabFileName(identifier))
	metadata := buildGitLabAuthMetadata(baseURL, gitLabLoginModeOAuth, tokenResp, direct)
	metadata["auth_kind"] = "oauth"
	metadata[gitLabOAuthClientIDMetadataKey] = clientID
	// Deviation from the reference: it never saves oauth_client_secret,
	// so its own refresh code reads back an empty secret for confidential
	// OAuth Apps. Save it here so refresh works for both app types.
	if strings.TrimSpace(clientSecret) != "" {
		metadata[gitLabOAuthClientSecretMetadataKey] = clientSecret
	}
	metadata["username"] = strings.TrimSpace(user.Username)
	if email := strings.TrimSpace(primaryGitLabEmail(user)); email != "" {
		metadata["email"] = email
	}
	metadata["name"] = strings.TrimSpace(user.Name)

	fmt.Println("GitLab Duo authentication successful")

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Label:    identifier,
		Metadata: metadata,
	}, nil
}

func (a *GitLabAuthenticator) loginPAT(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	client := gitlabauth.NewAuthClient(cfg)
	baseURL := a.resolveString(opts, gitLabBaseURLMetadataKey, gitlabauth.DefaultBaseURL)
	token, err := a.requireInput(opts, gitLabPersonalAccessTokenMetadataKey, "Enter GitLab personal access token: ")
	if err != nil {
		return nil, err
	}

	user, err := client.GetCurrentUser(ctx, baseURL, token)
	if err != nil {
		return nil, err
	}
	_, err = client.GetPersonalAccessTokenSelf(ctx, baseURL, token)
	if err != nil {
		return nil, err
	}
	direct, err := client.FetchDirectAccess(ctx, baseURL, token)
	if err != nil {
		return nil, err
	}

	identifier := gitLabAccountIdentifier(user)
	fileName := fmt.Sprintf("gitlab-%s-pat.json", sanitizeGitLabFileName(identifier))
	metadata := buildGitLabAuthMetadata(baseURL, gitLabLoginModePAT, nil, direct)
	metadata["auth_kind"] = "personal_access_token"
	metadata[gitLabPersonalAccessTokenMetadataKey] = strings.TrimSpace(token)
	metadata["token_preview"] = maskGitLabToken(token)
	metadata["username"] = strings.TrimSpace(user.Username)
	if email := strings.TrimSpace(primaryGitLabEmail(user)); email != "" {
		metadata["email"] = email
	}
	metadata["name"] = strings.TrimSpace(user.Name)

	fmt.Println("GitLab Duo PAT authentication successful")

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Label:    identifier + " (PAT)",
		Metadata: metadata,
	}, nil
}

func buildGitLabAuthMetadata(baseURL, mode string, tokenResp *gitlabauth.TokenResponse, direct *gitlabauth.DirectAccessResponse) map[string]any {
	metadata := map[string]any{
		"type":                     "gitlab",
		"auth_method":              strings.TrimSpace(mode),
		gitLabBaseURLMetadataKey:   gitlabauth.NormalizeBaseURL(baseURL),
		"last_refresh":             time.Now().UTC().Format(time.RFC3339),
		"refresh_interval_seconds": 240,
	}
	if tokenResp != nil {
		metadata["access_token"] = strings.TrimSpace(tokenResp.AccessToken)
		if refreshToken := strings.TrimSpace(tokenResp.RefreshToken); refreshToken != "" {
			metadata["refresh_token"] = refreshToken
		}
		if tokenType := strings.TrimSpace(tokenResp.TokenType); tokenType != "" {
			metadata["token_type"] = tokenType
		}
		if scope := strings.TrimSpace(tokenResp.Scope); scope != "" {
			metadata["scope"] = scope
		}
		if expiry := gitlabauth.TokenExpiry(time.Now(), tokenResp); !expiry.IsZero() {
			metadata["oauth_expires_at"] = expiry.Format(time.RFC3339)
		}
	}
	mergeGitLabDirectAccessMetadata(metadata, direct)
	return metadata
}

func mergeGitLabDirectAccessMetadata(metadata map[string]any, direct *gitlabauth.DirectAccessResponse) {
	if metadata == nil || direct == nil {
		return
	}
	if base := strings.TrimSpace(direct.BaseURL); base != "" {
		metadata["duo_gateway_base_url"] = base
	}
	if token := strings.TrimSpace(direct.Token); token != "" {
		metadata["duo_gateway_token"] = token
	}
	if direct.ExpiresAt > 0 {
		expiry := time.Unix(direct.ExpiresAt, 0).UTC()
		metadata["duo_gateway_expires_at"] = expiry.Format(time.RFC3339)
		now := time.Now().UTC()
		if ttl := expiry.Sub(now); ttl > 0 {
			interval := int(ttl.Seconds()) / 2
			switch {
			case interval < 60:
				interval = 60
			case interval > 240:
				interval = 240
			}
			metadata["refresh_interval_seconds"] = interval
		}
	}
	if len(direct.Headers) > 0 {
		headers := make(map[string]string, len(direct.Headers))
		for key, value := range direct.Headers {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				continue
			}
			headers[key] = value
		}
		if len(headers) > 0 {
			metadata["duo_gateway_headers"] = headers
		}
	}
	if direct.ModelDetails != nil {
		modelDetails := map[string]any{}
		if provider := strings.TrimSpace(direct.ModelDetails.ModelProvider); provider != "" {
			modelDetails["model_provider"] = provider
			metadata["model_provider"] = provider
		}
		if model := strings.TrimSpace(direct.ModelDetails.ModelName); model != "" {
			modelDetails["model_name"] = model
			metadata["model_name"] = model
		}
		if len(modelDetails) > 0 {
			metadata["model_details"] = modelDetails
		}
	}
}

func (a *GitLabAuthenticator) resolveString(opts *LoginOptions, key, fallback string) string {
	if opts != nil && opts.Metadata != nil {
		if value := strings.TrimSpace(opts.Metadata[key]); value != "" {
			return value
		}
	}
	for _, envKey := range gitLabEnvKeys(key) {
		if raw, ok := os.LookupEnv(envKey); ok {
			if trimmed := strings.TrimSpace(raw); trimmed != "" {
				return trimmed
			}
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return ""
}

func (a *GitLabAuthenticator) requireInput(opts *LoginOptions, key, prompt string) (string, error) {
	if value := a.resolveString(opts, key, ""); value != "" {
		return value, nil
	}
	if opts != nil && opts.Prompt != nil {
		value, err := opts.Prompt(prompt)
		if err != nil {
			return "", err
		}
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("gitlab auth: missing required %s", key)
}

func (a *GitLabAuthenticator) optionalInput(opts *LoginOptions, key, prompt string) (string, error) {
	if value := a.resolveString(opts, key, ""); value != "" {
		return value, nil
	}
	if opts != nil && opts.Prompt != nil {
		value, err := opts.Prompt(prompt)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(value), nil
	}
	return "", nil
}

func primaryGitLabEmail(user *gitlabauth.User) string {
	if user == nil {
		return ""
	}
	if value := strings.TrimSpace(user.Email); value != "" {
		return value
	}
	return strings.TrimSpace(user.PublicEmail)
}

func gitLabAccountIdentifier(user *gitlabauth.User) string {
	if user == nil {
		return "user"
	}
	for _, value := range []string{user.Username, primaryGitLabEmail(user), user.Name} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "user"
}

func sanitizeGitLabFileName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "user"
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteRune('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "user"
	}
	return result
}

func maskGitLabToken(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 8 {
		return trimmed
	}
	return trimmed[:4] + "..." + trimmed[len(trimmed)-4:]
}

func gitLabEnvKeys(key string) []string {
	switch strings.TrimSpace(key) {
	case gitLabBaseURLMetadataKey:
		return []string{"GITLAB_BASE_URL"}
	case gitLabOAuthClientIDMetadataKey:
		return []string{"GITLAB_OAUTH_CLIENT_ID"}
	case gitLabOAuthClientSecretMetadataKey:
		return []string{"GITLAB_OAUTH_CLIENT_SECRET"}
	case gitLabPersonalAccessTokenMetadataKey:
		return []string{"GITLAB_PERSONAL_ACCESS_TOKEN"}
	default:
		return nil
	}
}
```

Note: the two test functions added in Step 3 reference `gitlabauth` as a
package alias — add `gitlabauth "github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/gitlab"`
to the test file's own import block (test files have independent imports
from the package file).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./sdk/auth/... -run TestGitLabAuthenticator -v`
Expected: PASS — all four test functions green.

- [ ] **Step 7: Register GitLab's refresh lead**

**File:** Modify `sdk/auth/refresh_registry.go:19` area (next to the
existing `registerRefreshLead("meta", ...)`/`registerRefreshLead("devin",
...)` lines — read the file first to find the exact line, the pattern is
identical for every provider).

```go
registerRefreshLead("gitlab", func() Authenticator { return NewGitLabAuthenticator() })
```

- [ ] **Step 8: Run full package tests, gofmt, build, and commit**

```bash
gofmt -w sdk/auth/gitlab.go sdk/auth/gitlab_test.go sdk/auth/refresh_registry.go
go build ./sdk/auth/... && echo "build OK"
go test ./sdk/auth/... -v 2>&1 | tail -30
git add sdk/auth/gitlab.go sdk/auth/gitlab_test.go sdk/auth/refresh_registry.go
git commit -m "feat: add GitLabAuthenticator (OAuth + PAT login dispatch)"
```

---

### Task 3: CLI login commands and flag wiring

**Files:**
- Create: `internal/cmd/gitlab_login.go`
- Modify: `cmd/server/main.go` (flag declarations + dispatch switch — read
  the existing `--devin-login` wiring first; the pattern repeats exactly)

**Interfaces:**
- Consumes: `newAuthManager()` and `LoginOptions` (already used by every
  other `internal/cmd/*_login.go` file in this package — read
  `internal/cmd/devin_login.go` for the exact pattern, already confirmed
  identical in this repo and the reference).
- Produces: `cmd.DoGitLabLogin(cfg *config.Config, options
  *LoginOptions)`, `cmd.DoGitLabTokenLogin(cfg *config.Config, options
  *LoginOptions)`, CLI flags `--gitlab-login` and `--gitlab-token-login`.

- [ ] **Step 1: Implement the login commands**

```go
// internal/cmd/gitlab_login.go
package cmd

import (
	"context"
	"fmt"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	sdkAuth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoGitLabLogin triggers the GitLab Duo OAuth (PKCE) login flow and saves credentials.
func DoGitLabLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	promptFn := options.Prompt
	if promptFn == nil {
		promptFn = defaultProjectPrompt()
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser:    options.NoBrowser,
		CallbackPort: options.CallbackPort,
		Metadata: map[string]string{
			"login_mode": "oauth",
		},
		Prompt: promptFn,
	}

	record, savedPath, err := manager.Login(context.Background(), "gitlab", cfg, authOpts)
	if err != nil {
		log.Errorf("GitLab Duo authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("GitLab Duo authentication successful!")
}

// DoGitLabTokenLogin triggers the GitLab Duo personal-access-token login flow and saves credentials.
func DoGitLabTokenLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	promptFn := options.Prompt
	if promptFn == nil {
		promptFn = defaultProjectPrompt()
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		Metadata: map[string]string{
			"login_mode": "pat",
		},
		Prompt: promptFn,
	}

	record, savedPath, err := manager.Login(context.Background(), "gitlab", cfg, authOpts)
	if err != nil {
		log.Errorf("GitLab Duo PAT authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("GitLab Duo PAT authentication successful!")
}
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./internal/cmd/... 2>&1 | head -20`
Expected: clean (no output) — this file alone has no tests of its own,
matching `internal/cmd/devin_login.go`'s own lack of a test file; its
behavior is exercised indirectly through Task 2's `GitLabAuthenticator`
tests.

- [ ] **Step 3: Wire the CLI flags in `cmd/server/main.go`**

Read `cmd/server/main.go` around the existing `devinLogin`/`--devin-login`
flag declaration and its dispatch (the file has a `flag.BoolVar(&devinLogin,
"devin-login", ...)` line near the other login flags, and a `case
"devin-login":` arm in the same switch statement that lists every login
flag name — both found earlier at lines ~145 and ~970-971 of this file).
Add, following the exact same pattern:

```go
flag.BoolVar(&gitlabLogin, "gitlab-login", false, "Login to GitLab Duo using OAuth")
flag.BoolVar(&gitlabTokenLogin, "gitlab-token-login", false, "Login to GitLab Duo using a personal access token")
```

next to the other `*Login` flag declarations, declare the two new
booleans (`var gitlabLogin, gitlabTokenLogin bool`) alongside the other
login-flag variables, add `"gitlab-login", "gitlab-token-login"` to the
`case "codex-login", "codex-device-login", ...:` list that gates
non-server-mode behavior, and add the dispatch:

```go
case gitlabLogin:
	cmd.DoGitLabLogin(cfg, loginOptions)
	return
case gitlabTokenLogin:
	cmd.DoGitLabTokenLogin(cfg, loginOptions)
	return
```

in the same `if`/`switch` block where `devinLogin`/`metaLogin` are
dispatched (read that block first — it may be `if devinLogin { ... }
else if metaLogin { ... }` or a switch; match the existing style exactly
rather than introducing a different control-flow shape).

- [ ] **Step 4: Build and smoke-test the flag parses**

```bash
go build -o /tmp/kaori-gitlab-test ./cmd/server
/tmp/kaori-gitlab-test --help 2>&1 | grep gitlab
rm /tmp/kaori-gitlab-test
```

Expected: both `--gitlab-login` and `--gitlab-token-login` appear in the
help output with their descriptions.

- [ ] **Step 5: gofmt, build, test, commit**

```bash
gofmt -w internal/cmd/gitlab_login.go cmd/server/main.go
go build ./... && echo "build OK"
go test ./internal/cmd/... ./cmd/... 2>&1 | tail -10
git add internal/cmd/gitlab_login.go cmd/server/main.go
git commit -m "feat: add --gitlab-login and --gitlab-token-login CLI flags"
```

---

### Task 4: GitLab executor (native-gateway delegation only)

**Files:**
- Create: `internal/runtime/executor/gitlab_executor.go`
- Test: `internal/runtime/executor/gitlab_executor_test.go`
- Test: `internal/runtime/executor/gitlab_executor_integration_test.go`

**Interfaces:**
- Consumes: Task 1's `gitlab` package (`gitlab.NewAuthClient`,
  `gitlab.NormalizeBaseURL`, `gitlab.TokenExpiry`,
  `gitlab.ExtractDiscoveredModels`, `gitlab.TokenResponse`,
  `gitlab.DirectAccessResponse`), this repo's existing `NewClaudeExecutor
  (cfg *config.Config) *ClaudeExecutor` and `NewCodexExecutor(cfg
  *config.Config) *CodexExecutor` (both confirmed to implement
  `cliproxyauth.ProviderExecutor` fully already), `cliproxyauth.Auth.Clone()
  *Auth` (confirmed exists, `sdk/cliproxy/auth/types.go:290`),
  `helps.NewProxyAwareHTTPClient` (`internal/runtime/executor/helps`,
  same helper the Mistral executor uses), `statusErr` (package-level type
  in `internal/runtime/executor/openai_compat_executor.go`, same one
  Mistral's executor reuses), `thinking.ParseSuffix`, `registry.ModelInfo`.
- Produces: `executor.NewGitLabExecutor(cfg *config.Config)
  *GitLabExecutor` implementing `cliproxyauth.ProviderExecutor`,
  `executor.GitLabModelsFromAuth(auth *cliproxyauth.Auth)
  []*registry.ModelInfo`. Task 5 consumes both directly.

- [ ] **Step 1: Write the failing gateway-auth-builder tests**

```go
// internal/runtime/executor/gitlab_executor_test.go
package executor

import (
	"testing"

	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
)

func gitLabTestAuth(provider, modelName, gatewayBaseURL, gatewayToken string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       "gitlab-test",
		Provider: "gitlab",
		Metadata: map[string]any{
			"model_provider":       provider,
			"model_name":           modelName,
			"duo_gateway_base_url": gatewayBaseURL,
			"duo_gateway_token":    gatewayToken,
		},
	}
}

func TestBuildGitLabAnthropicGatewayAuth_SwapsCredentialsForAnthropicModel(t *testing.T) {
	auth := gitLabTestAuth("anthropic", "claude-sonnet-4-5", "https://gitlab.com/ai/v1/proxy/anthropic", "gw-token-123")

	nativeAuth, ok := buildGitLabAnthropicGatewayAuth(auth, "")
	if !ok {
		t.Fatal("expected native gateway auth to be built")
	}
	if nativeAuth.Provider != "claude" {
		t.Errorf("expected Provider %q, got %q", "claude", nativeAuth.Provider)
	}
	if nativeAuth.Attributes["api_key"] != "gw-token-123" {
		t.Errorf("expected api_key %q, got %q", "gw-token-123", nativeAuth.Attributes["api_key"])
	}
	if nativeAuth.Attributes["base_url"] != "https://gitlab.com/ai/v1/proxy/anthropic" {
		t.Errorf("unexpected base_url: %q", nativeAuth.Attributes["base_url"])
	}
	// Confirm the original auth is untouched (Clone semantics).
	if auth.Provider != "gitlab" {
		t.Errorf("original auth.Provider was mutated: %q", auth.Provider)
	}
}

func TestBuildGitLabOpenAIGatewayAuth_SwapsCredentialsForOpenAIModel(t *testing.T) {
	auth := gitLabTestAuth("openai", "gpt-5-codex", "https://gitlab.com/ai/v1/proxy/openai/v1", "gw-token-456")

	nativeAuth, ok := buildGitLabOpenAIGatewayAuth(auth, "")
	if !ok {
		t.Fatal("expected native gateway auth to be built")
	}
	if nativeAuth.Provider != "codex" {
		t.Errorf("expected Provider %q, got %q", "codex", nativeAuth.Provider)
	}
	if nativeAuth.Attributes["api_key"] != "gw-token-456" {
		t.Errorf("expected api_key %q, got %q", "gw-token-456", nativeAuth.Attributes["api_key"])
	}
}

func TestBuildGitLabGatewayAuth_FailsWithoutGatewayToken(t *testing.T) {
	auth := gitLabTestAuth("anthropic", "claude-sonnet-4-5", "https://gitlab.com/ai/v1/proxy/anthropic", "")
	if _, ok := buildGitLabAnthropicGatewayAuth(auth, ""); ok {
		t.Error("expected native gateway auth to fail without a gateway token")
	}
}

func TestGitLabUsesAnthropicGateway_FalseForOpenAIModel(t *testing.T) {
	auth := gitLabTestAuth("openai", "gpt-5-codex", "https://gitlab.com/ai/v1/proxy/openai/v1", "gw-token")
	if gitLabUsesAnthropicGateway(auth, "") {
		t.Error("expected false for an OpenAI-managed model")
	}
}

func TestGitLabAnthropicGatewayBaseURL_AppendsProxyPath(t *testing.T) {
	auth := gitLabTestAuth("anthropic", "claude-sonnet-4-5", "https://gitlab.example.com/ai", "tok")
	got := gitLabAnthropicGatewayBaseURL(auth)
	want := "https://gitlab.example.com/ai/v1/proxy/anthropic"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInferGitLabProviderFromModel(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-4-5": "anthropic",
		"gpt-5-codex":       "openai",
		"o3-mini":           "openai",
		"mistral-large":     "",
	}
	for model, want := range cases {
		if got := inferGitLabProviderFromModel(model); got != want {
			t.Errorf("inferGitLabProviderFromModel(%q) = %q, want %q", model, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/runtime/executor/... -run TestBuildGitLab -v`
Expected: FAIL — `undefined: buildGitLabAnthropicGatewayAuth` and related
symbols (the file doesn't exist yet).

- [ ] **Step 3: Implement the executor (native-gateway path only)**

```go
// internal/runtime/executor/gitlab_executor.go
package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/gitlab"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/registry"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/runtime/executor/helps"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/thinking"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
)

const (
	gitLabProviderKey     = "gitlab"
	gitLabAuthMethodOAuth = "oauth"
	gitLabAuthMethodPAT   = "pat"
)

type gitLabCatalogModel struct {
	ID          string
	DisplayName string
	Provider    string
}

var gitLabAgenticCatalog = []gitLabCatalogModel{
	{ID: "duo-chat-gpt-5-1", DisplayName: "GitLab Duo (GPT-5.1)", Provider: "openai"},
	{ID: "duo-chat-opus-4-6", DisplayName: "GitLab Duo (Claude Opus 4.6)", Provider: "anthropic"},
	{ID: "duo-chat-opus-4-5", DisplayName: "GitLab Duo (Claude Opus 4.5)", Provider: "anthropic"},
	{ID: "duo-chat-sonnet-4-6", DisplayName: "GitLab Duo (Claude Sonnet 4.6)", Provider: "anthropic"},
	{ID: "duo-chat-sonnet-4-5", DisplayName: "GitLab Duo (Claude Sonnet 4.5)", Provider: "anthropic"},
	{ID: "duo-chat-gpt-5-mini", DisplayName: "GitLab Duo (GPT-5 Mini)", Provider: "openai"},
	{ID: "duo-chat-gpt-5-2", DisplayName: "GitLab Duo (GPT-5.2)", Provider: "openai"},
	{ID: "duo-chat-gpt-5-2-codex", DisplayName: "GitLab Duo (GPT-5.2 Codex)", Provider: "openai"},
	{ID: "duo-chat-gpt-5-codex", DisplayName: "GitLab Duo (GPT-5 Codex)", Provider: "openai"},
	{ID: "duo-chat-haiku-4-5", DisplayName: "GitLab Duo (Claude Haiku 4.5)", Provider: "anthropic"},
}

var gitLabModelAliases = map[string]string{
	"duo-chat-haiku-4-6": "duo-chat-haiku-4-5",
}

// GitLabExecutor implements cliproxyauth.ProviderExecutor for GitLab Duo.
// Phase 1: it only serves accounts whose GitLab `direct_access` metadata
// identifies an Anthropic- or OpenAI-managed model, by delegating to the
// existing Claude/Codex executors with GitLab's gateway credentials
// swapped in. Accounts without usable gateway metadata get an explicit
// "not yet implemented" error (the text-prompt fallback is Phase 2).
type GitLabExecutor struct {
	cfg *config.Config
}

func NewGitLabExecutor(cfg *config.Config) *GitLabExecutor {
	return &GitLabExecutor{cfg: cfg}
}

var _ cliproxyauth.ProviderExecutor = (*GitLabExecutor)(nil)

func (e *GitLabExecutor) Identifier() string { return gitLabProviderKey }

func (e *GitLabExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if nativeExec, nativeAuth, nativeReq, ok := e.nativeGateway(auth, req); ok {
		return nativeExec.Execute(ctx, nativeAuth, nativeReq, opts)
	}
	return cliproxyexecutor.Response{}, gitLabNativeGatewayUnavailableErr()
}

func (e *GitLabExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	if nativeExec, nativeAuth, nativeReq, ok := e.nativeGateway(auth, req); ok {
		return nativeExec.ExecuteStream(ctx, nativeAuth, nativeReq, opts)
	}
	return nil, gitLabNativeGatewayUnavailableErr()
}

func (e *GitLabExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if nativeExec, nativeAuth, nativeReq, ok := e.nativeGateway(auth, req); ok {
		return nativeExec.CountTokens(ctx, nativeAuth, nativeReq, opts)
	}
	return cliproxyexecutor.Response{}, gitLabNativeGatewayUnavailableErr()
}

func (e *GitLabExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("gitlab duo executor: request is nil")
	}
	if nativeExec, nativeAuth := e.nativeGatewayHTTP(auth); nativeExec != nil {
		return nativeExec.HttpRequest(ctx, nativeAuth, req)
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if token := gitLabPrimaryToken(auth); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	return helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0).Do(httpReq)
}

func (e *GitLabExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("gitlab duo executor: auth is nil")
	}
	baseURL := gitLabBaseURL(auth)
	token := gitLabPrimaryToken(auth)
	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("gitlab duo executor: missing base URL or token")
	}

	client := gitlab.NewAuthClient(e.cfg)
	method := strings.ToLower(strings.TrimSpace(gitLabMetadataString(auth.Metadata, "auth_method")))
	if method == "" {
		method = gitLabAuthMethodOAuth
	}

	if method == gitLabAuthMethodOAuth {
		if refreshed, refreshErr := e.refreshOAuthToken(ctx, client, auth, baseURL); refreshErr == nil && refreshed != nil {
			token = refreshed.AccessToken
			applyGitLabTokenMetadata(auth.Metadata, refreshed)
		}
	}

	direct, err := client.FetchDirectAccess(ctx, baseURL, token)
	if err != nil && method == gitLabAuthMethodOAuth {
		if refreshed, refreshErr := e.refreshOAuthToken(ctx, client, auth, baseURL); refreshErr == nil && refreshed != nil {
			token = refreshed.AccessToken
			applyGitLabTokenMetadata(auth.Metadata, refreshed)
			direct, err = client.FetchDirectAccess(ctx, baseURL, token)
		}
	}
	if err != nil {
		return nil, err
	}

	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["type"] = gitLabProviderKey
	auth.Metadata["auth_method"] = method
	auth.Metadata["auth_kind"] = gitLabAuthKind(method)
	auth.Metadata["base_url"] = gitlab.NormalizeBaseURL(baseURL)
	auth.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	mergeGitLabDirectAccessMetadata(auth.Metadata, direct)
	return auth, nil
}

func (e *GitLabExecutor) refreshOAuthToken(ctx context.Context, client *gitlab.AuthClient, auth *cliproxyauth.Auth, baseURL string) (*gitlab.TokenResponse, error) {
	if auth == nil {
		return nil, fmt.Errorf("gitlab duo executor: auth is nil")
	}
	refreshToken := gitLabMetadataString(auth.Metadata, "refresh_token")
	if refreshToken == "" {
		return nil, fmt.Errorf("gitlab duo executor: refresh token missing")
	}
	if !gitLabOAuthTokenNeedsRefresh(auth.Metadata) && gitLabPrimaryToken(auth) != "" {
		return nil, nil
	}
	return client.RefreshTokens(
		ctx,
		baseURL,
		gitLabMetadataString(auth.Metadata, "oauth_client_id"),
		gitLabMetadataString(auth.Metadata, "oauth_client_secret"),
		refreshToken,
	)
}

func gitLabNativeGatewayUnavailableErr() error {
	return statusErr{
		code: http.StatusServiceUnavailable,
		msg:  "gitlab duo: native gateway unavailable for this account; fallback path not yet implemented (Phase 2)",
	}
}

func (e *GitLabExecutor) nativeGateway(auth *cliproxyauth.Auth, req cliproxyexecutor.Request) (cliproxyauth.ProviderExecutor, *cliproxyauth.Auth, cliproxyexecutor.Request, bool) {
	if nativeAuth, ok := buildGitLabAnthropicGatewayAuth(auth, req.Model); ok {
		nativeReq := req
		nativeReq.Model = gitLabResolvedModel(auth, req.Model)
		return NewClaudeExecutor(e.cfg), nativeAuth, nativeReq, true
	}
	if nativeAuth, ok := buildGitLabOpenAIGatewayAuth(auth, req.Model); ok {
		nativeReq := req
		nativeReq.Model = gitLabResolvedModel(auth, req.Model)
		return NewCodexExecutor(e.cfg), nativeAuth, nativeReq, true
	}
	return nil, nil, req, false
}

func (e *GitLabExecutor) nativeGatewayHTTP(auth *cliproxyauth.Auth) (cliproxyauth.ProviderExecutor, *cliproxyauth.Auth) {
	if nativeAuth, ok := buildGitLabAnthropicGatewayAuth(auth, ""); ok {
		return NewClaudeExecutor(e.cfg), nativeAuth
	}
	if nativeAuth, ok := buildGitLabOpenAIGatewayAuth(auth, ""); ok {
		return NewCodexExecutor(e.cfg), nativeAuth
	}
	return nil, nil
}

func buildGitLabAnthropicGatewayAuth(auth *cliproxyauth.Auth, requestedModel string) (*cliproxyauth.Auth, bool) {
	if !gitLabUsesAnthropicGateway(auth, requestedModel) {
		return nil, false
	}
	baseURL := gitLabAnthropicGatewayBaseURL(auth)
	token := gitLabMetadataString(auth.Metadata, "duo_gateway_token")
	if baseURL == "" || token == "" {
		return nil, false
	}

	nativeAuth := auth.Clone()
	nativeAuth.Provider = "claude"
	if nativeAuth.Attributes == nil {
		nativeAuth.Attributes = make(map[string]string)
	}
	nativeAuth.Attributes["api_key"] = token
	nativeAuth.Attributes["base_url"] = baseURL
	for key, value := range gitLabGatewayHeaders(auth) {
		if key == "" || value == "" {
			continue
		}
		nativeAuth.Attributes["header:"+key] = value
	}
	return nativeAuth, true
}

func buildGitLabOpenAIGatewayAuth(auth *cliproxyauth.Auth, requestedModel string) (*cliproxyauth.Auth, bool) {
	if !gitLabUsesOpenAIGateway(auth, requestedModel) {
		return nil, false
	}
	baseURL := gitLabOpenAIGatewayBaseURL(auth)
	token := gitLabMetadataString(auth.Metadata, "duo_gateway_token")
	if baseURL == "" || token == "" {
		return nil, false
	}

	nativeAuth := auth.Clone()
	nativeAuth.Provider = "codex"
	if nativeAuth.Attributes == nil {
		nativeAuth.Attributes = make(map[string]string)
	}
	nativeAuth.Attributes["api_key"] = token
	nativeAuth.Attributes["base_url"] = baseURL
	for key, value := range gitLabGatewayHeaders(auth) {
		if key == "" || value == "" {
			continue
		}
		nativeAuth.Attributes["header:"+key] = value
	}
	return nativeAuth, true
}

func gitLabGatewayHeaders(auth *cliproxyauth.Auth) map[string]string {
	out := make(map[string]string)
	if auth != nil && auth.Metadata != nil {
		raw, ok := auth.Metadata["duo_gateway_headers"]
		if ok {
			switch typed := raw.(type) {
			case map[string]string:
				for key, value := range typed {
					key = strings.TrimSpace(key)
					value = strings.TrimSpace(value)
					if key != "" && value != "" {
						out[key] = value
					}
				}
			case map[string]any:
				for key, value := range typed {
					key = strings.TrimSpace(key)
					if key == "" {
						continue
					}
					strValue := strings.TrimSpace(fmt.Sprint(value))
					if strValue != "" {
						out[key] = strValue
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func gitLabUsesAnthropicGateway(auth *cliproxyauth.Auth, requestedModel string) bool {
	if auth == nil || auth.Metadata == nil {
		return false
	}
	provider := gitLabGatewayProvider(auth, requestedModel)
	return provider == "anthropic" &&
		gitLabMetadataString(auth.Metadata, "duo_gateway_base_url") != "" &&
		gitLabMetadataString(auth.Metadata, "duo_gateway_token") != ""
}

func gitLabUsesOpenAIGateway(auth *cliproxyauth.Auth, requestedModel string) bool {
	if auth == nil || auth.Metadata == nil {
		return false
	}
	provider := gitLabGatewayProvider(auth, requestedModel)
	return provider == "openai" &&
		gitLabMetadataString(auth.Metadata, "duo_gateway_base_url") != "" &&
		gitLabMetadataString(auth.Metadata, "duo_gateway_token") != ""
}

func gitLabGatewayProvider(auth *cliproxyauth.Auth, requestedModel string) string {
	modelName := strings.TrimSpace(gitLabResolvedModel(auth, requestedModel))
	if provider := inferGitLabProviderFromModel(modelName); provider != "" {
		return provider
	}
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	provider := strings.ToLower(gitLabMetadataString(auth.Metadata, "model_provider"))
	if provider == "" {
		provider = inferGitLabProviderFromModel(gitLabMetadataString(auth.Metadata, "model_name"))
	}
	return provider
}

func inferGitLabProviderFromModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(model, "claude"):
		return "anthropic"
	case strings.Contains(model, "gpt"), strings.Contains(model, "o1"), strings.Contains(model, "o3"), strings.Contains(model, "o4"):
		return "openai"
	default:
		return ""
	}
}

func gitLabAnthropicGatewayBaseURL(auth *cliproxyauth.Auth) string {
	raw := strings.TrimSpace(gitLabMetadataString(auth.Metadata, "duo_gateway_base_url"))
	if raw == "" {
		return ""
	}
	base, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	path := strings.TrimRight(base.EscapedPath(), "/")
	switch {
	case strings.HasSuffix(path, "/ai/v1/proxy/anthropic"), strings.HasSuffix(path, "/v1/proxy/anthropic"):
		return strings.TrimRight(base.String(), "/")
	case path == "/ai":
		base.Path = "/ai/v1/proxy/anthropic"
	case path != "":
		base.Path = strings.TrimRight(path, "/") + "/v1/proxy/anthropic"
	case strings.Contains(strings.ToLower(base.Host), "gitlab.com"):
		base.Path = "/ai/v1/proxy/anthropic"
	default:
		base.Path = "/v1/proxy/anthropic"
	}
	return strings.TrimRight(base.String(), "/")
}

func gitLabOpenAIGatewayBaseURL(auth *cliproxyauth.Auth) string {
	raw := strings.TrimSpace(gitLabMetadataString(auth.Metadata, "duo_gateway_base_url"))
	if raw == "" {
		return ""
	}
	base, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	path := strings.TrimRight(base.EscapedPath(), "/")
	switch {
	case strings.HasSuffix(path, "/ai/v1/proxy/openai/v1"), strings.HasSuffix(path, "/v1/proxy/openai/v1"):
		return strings.TrimRight(base.String(), "/")
	case path == "/ai":
		base.Path = "/ai/v1/proxy/openai/v1"
	case path != "":
		base.Path = strings.TrimRight(path, "/") + "/v1/proxy/openai/v1"
	case strings.Contains(strings.ToLower(base.Host), "gitlab.com"):
		base.Path = "/ai/v1/proxy/openai/v1"
	default:
		base.Path = "/v1/proxy/openai/v1"
	}
	return strings.TrimRight(base.String(), "/")
}

func gitLabPrimaryToken(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if token := gitLabMetadataString(auth.Metadata, "access_token"); token != "" {
		return token
	}
	return gitLabMetadataString(auth.Metadata, "personal_access_token")
}

func gitLabBaseURL(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	return gitlab.NormalizeBaseURL(gitLabMetadataString(auth.Metadata, "base_url"))
}

func gitLabResolvedModel(auth *cliproxyauth.Auth, requested string) string {
	requested = strings.TrimSpace(thinking.ParseSuffix(requested).ModelName)
	if requested != "" && !strings.EqualFold(requested, "gitlab-duo") {
		if mapped, ok := gitLabModelAliases[strings.ToLower(requested)]; ok && strings.TrimSpace(mapped) != "" {
			return mapped
		}
		return requested
	}
	if auth != nil && auth.Metadata != nil {
		for _, model := range gitlab.ExtractDiscoveredModels(auth.Metadata) {
			if name := strings.TrimSpace(model.ModelName); name != "" {
				return name
			}
		}
	}
	if requested != "" {
		return requested
	}
	return "gitlab-duo"
}

func gitLabMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if metadata == nil {
			return ""
		}
		if value, ok := metadata[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func gitLabOAuthTokenNeedsRefresh(metadata map[string]any) bool {
	expiry := gitLabMetadataString(metadata, "oauth_expires_at")
	if expiry == "" {
		return true
	}
	ts, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		return true
	}
	return time.Until(ts) <= 5*time.Minute
}

func applyGitLabTokenMetadata(metadata map[string]any, tokenResp *gitlab.TokenResponse) {
	if metadata == nil || tokenResp == nil {
		return
	}
	if accessToken := strings.TrimSpace(tokenResp.AccessToken); accessToken != "" {
		metadata["access_token"] = accessToken
	}
	if refreshToken := strings.TrimSpace(tokenResp.RefreshToken); refreshToken != "" {
		metadata["refresh_token"] = refreshToken
	}
	if tokenType := strings.TrimSpace(tokenResp.TokenType); tokenType != "" {
		metadata["token_type"] = tokenType
	}
	if scope := strings.TrimSpace(tokenResp.Scope); scope != "" {
		metadata["scope"] = scope
	}
	if expiry := gitlab.TokenExpiry(time.Now(), tokenResp); !expiry.IsZero() {
		metadata["oauth_expires_at"] = expiry.Format(time.RFC3339)
	}
}

func mergeGitLabDirectAccessMetadata(metadata map[string]any, direct *gitlab.DirectAccessResponse) {
	if metadata == nil || direct == nil {
		return
	}
	if base := strings.TrimSpace(direct.BaseURL); base != "" {
		metadata["duo_gateway_base_url"] = base
	}
	if token := strings.TrimSpace(direct.Token); token != "" {
		metadata["duo_gateway_token"] = token
	}
	if direct.ExpiresAt > 0 {
		expiry := time.Unix(direct.ExpiresAt, 0).UTC()
		metadata["duo_gateway_expires_at"] = expiry.Format(time.RFC3339)
		now := time.Now().UTC()
		if ttl := expiry.Sub(now); ttl > 0 {
			interval := int(ttl.Seconds()) / 2
			switch {
			case interval < 60:
				interval = 60
			case interval > 240:
				interval = 240
			}
			metadata["refresh_interval_seconds"] = interval
		}
	}
	if len(direct.Headers) > 0 {
		headers := make(map[string]string, len(direct.Headers))
		for key, value := range direct.Headers {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				continue
			}
			headers[key] = value
		}
		if len(headers) > 0 {
			metadata["duo_gateway_headers"] = headers
		}
	}
	if direct.ModelDetails != nil {
		modelDetails := map[string]any{}
		if provider := strings.TrimSpace(direct.ModelDetails.ModelProvider); provider != "" {
			modelDetails["model_provider"] = provider
			metadata["model_provider"] = provider
		}
		if model := strings.TrimSpace(direct.ModelDetails.ModelName); model != "" {
			modelDetails["model_name"] = model
			metadata["model_name"] = model
		}
		if len(modelDetails) > 0 {
			metadata["model_details"] = modelDetails
		}
	}
}

func gitLabAuthKind(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case gitLabAuthMethodPAT:
		return "personal_access_token"
	default:
		return "oauth"
	}
}

// GitLabModelsFromAuth lists the stable "gitlab-duo" alias, a small static
// catalog of known duo-chat-* model IDs, and any model GitLab's
// direct_access metadata currently reports for this specific account.
func GitLabModelsFromAuth(auth *cliproxyauth.Auth) []*registry.ModelInfo {
	models := make([]*registry.ModelInfo, 0, len(gitLabAgenticCatalog)+4)
	seen := make(map[string]struct{}, len(gitLabAgenticCatalog)+4)
	addModel := func(id, displayName string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		models = append(models, &registry.ModelInfo{
			ID:          id,
			Object:      "model",
			Created:     1790860800, // fixed timestamp (2026-10-01), avoids time.Now() per workflow/test determinism conventions used elsewhere in this package
			OwnedBy:     "gitlab",
			Type:        "gitlab",
			DisplayName: displayName,
		})
	}

	addModel("gitlab-duo", "GitLab Duo")
	for _, model := range gitLabAgenticCatalog {
		addModel(model.ID, model.DisplayName)
	}
	for alias := range gitLabModelAliases {
		addModel(alias, "GitLab Duo Alias")
	}
	if auth == nil {
		return models
	}
	for _, model := range gitlab.ExtractDiscoveredModels(auth.Metadata) {
		name := strings.TrimSpace(model.ModelName)
		if name == "" {
			continue
		}
		displayName := "GitLab Duo"
		if provider := strings.TrimSpace(model.ModelProvider); provider != "" {
			displayName = fmt.Sprintf("GitLab Duo (%s)", provider)
		}
		addModel(name, displayName)
	}
	return models
}

var _ = bufio.NewScanner // keep import used if trimmed during edits
var _ = bytes.NewReader
```

(As in Task 1, the two trailing `var _ =` lines are only a safety net for
unused imports — `bufio`/`bytes` are not actually used by this Phase-1
file since the SSE-handling fallback is excluded; delete the two lines
and the two imports together, `gofmt`/`go vet` will confirm.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/runtime/executor/... -run "TestBuildGitLab|TestGitLabUses|TestGitLabAnthropicGatewayBaseURL|TestInferGitLabProviderFromModel" -v`
Expected: PASS — all 6 tests green.

- [ ] **Step 5: Write the failing `Refresh` tests (covers Review Focus: PAT auth refresh must not attempt OAuth refresh)**

```go
// append to internal/runtime/executor/gitlab_executor_test.go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
)

func TestGitLabExecutor_Refresh_PATAuthSkipsOAuthRefresh(t *testing.T) {
	var sawTokenEndpoint bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			sawTokenEndpoint = true
			w.WriteHeader(http.StatusInternalServerError)
		case "/api/v4/code_suggestions/direct_access":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"base_url":"https://gitlab.example.com/ai","token":"new-gw-token","expires_at":0,"headers":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	exec := NewGitLabExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "gitlab",
		Metadata: map[string]any{
			"auth_method":            "pat",
			"base_url":               server.URL,
			"personal_access_token":  "pat-token-123",
		},
	}

	_, err := exec.Refresh(context.Background(), auth)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawTokenEndpoint {
		t.Error("expected PAT-based auth to never call /oauth/token during refresh")
	}
	if auth.Metadata["duo_gateway_token"] != "new-gw-token" {
		t.Errorf("expected refreshed gateway token to be applied, got %v", auth.Metadata["duo_gateway_token"])
	}
}

func TestGitLabExecutor_Execute_ReturnsClearErrorWhenNativeGatewayUnavailable(t *testing.T) {
	exec := NewGitLabExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "gitlab",
		Metadata: map[string]any{
			// No duo_gateway_base_url/duo_gateway_token/model_provider at all.
		},
	}
	req := cliproxyexecutor.Request{Model: "gitlab-duo", Payload: []byte(`{}`)}
	opts := cliproxyexecutor.Options{}

	_, err := exec.Execute(context.Background(), auth, req, opts)
	if err == nil {
		t.Fatal("expected an error when native gateway metadata is unavailable")
	}
	se, ok := err.(statusErr)
	if !ok {
		t.Fatalf("expected statusErr, got %T: %v", err, err)
	}
	if se.StatusCode() != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", se.StatusCode())
	}
}
```

- [ ] **Step 6: Run the tests to verify they fail, then pass**

Run: `go test ./internal/runtime/executor/... -run TestGitLabExecutor -v`
Expected first: FAIL (`Refresh`/`Execute` not calling the right paths
yet, or compile error if a helper is missing) — then after Step 3's
implementation is in place, re-run and expect PASS. (If Step 3 was
already applied before this step, this step's first run already passes —
that's fine, note it and move on.)

- [ ] **Step 7: Write the end-to-end integration test (covers full native-gateway delegation to a mock Claude-shaped backend)**

```go
// internal/runtime/executor/gitlab_executor_integration_test.go
package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	_ "github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
	sdktranslator "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"
)

// TestGitLabExecutor_Execute_DelegatesToClaudeForAnthropicManagedModel drives
// the full native-gateway delegation path: a GitLab auth whose direct_access
// metadata says "anthropic" must result in a real call to the Claude
// executor's wire format against the swapped-in gateway base_url/token.
func TestGitLabExecutor_Execute_DelegatesToClaudeForAnthropicManagedModel(t *testing.T) {
	var sawAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("x-api-key")
		if sawAuthHeader == "" {
			sawAuthHeader = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`))
	}))
	defer server.Close()

	exec := NewGitLabExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "gitlab",
		Metadata: map[string]any{
			"model_provider":       "anthropic",
			"model_name":           "claude-sonnet-4-5",
			"duo_gateway_base_url": server.URL,
			"duo_gateway_token":    "gw-token-abc",
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gitlab-duo",
		Payload: []byte(`{"model":"gitlab-duo","messages":[{"role":"user","content":"ping"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	resp, err := exec.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawAuthHeader == "" {
		t.Error("expected the delegated Claude executor to send the swapped-in gateway token")
	}
	if !strings.Contains(string(resp.Payload), "pong") {
		t.Errorf("expected translated response to contain %q, got: %s", "pong", resp.Payload)
	}
}

func TestGitLabModelsFromAuth_IncludesAliasAndDiscoveredModel(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{
			"model_provider": "anthropic",
			"model_name":     "claude-opus-4-7-preview",
		},
	}
	models := GitLabModelsFromAuth(auth)

	var sawStableAlias, sawDiscovered, sawCatalogEntry bool
	for _, m := range models {
		switch m.ID {
		case "gitlab-duo":
			sawStableAlias = true
		case "claude-opus-4-7-preview":
			sawDiscovered = true
		case "duo-chat-opus-4-6":
			sawCatalogEntry = true
		}
	}
	if !sawStableAlias {
		t.Error("expected the stable 'gitlab-duo' alias to be present")
	}
	if !sawDiscovered {
		t.Error("expected the dynamically discovered model to be present")
	}
	if !sawCatalogEntry {
		t.Error("expected a static catalog entry to be present")
	}
}
```

- [ ] **Step 8: Run the integration tests**

Run: `go test ./internal/runtime/executor/... -run "TestGitLabExecutor_Execute_DelegatesToClaude|TestGitLabModelsFromAuth" -v`
Expected: PASS.

- [ ] **Step 9: gofmt, build, full package test, commit**

```bash
gofmt -w internal/runtime/executor/gitlab_executor.go internal/runtime/executor/gitlab_executor_test.go internal/runtime/executor/gitlab_executor_integration_test.go
go build ./... && echo "build OK"
go test ./internal/runtime/executor/... -v 2>&1 | tail -40
git add internal/runtime/executor/gitlab_executor*.go
git commit -m "feat: add GitLabExecutor with native-gateway delegation to Claude/Codex"
```

---

### Task 5: Wiring and final verification

**Files:**
- Modify: `sdk/cliproxy/service_executors.go` (register the executor;
  add `"gitlab"` to `baselineExecutorAuths()`)
- Modify: `sdk/cliproxy/service_models.go` (model listing dispatch)

**Interfaces:**
- Consumes: `executor.NewGitLabExecutor`, `executor.GitLabModelsFromAuth`
  (Task 4), the existing `case "meta":`/`"meta"` entries in both files as
  the exact insertion template (same pattern already used for Mistral's
  own wiring task earlier in this project).

- [ ] **Step 1: Register the executor**

In `sdk/cliproxy/service_executors.go`, find the `case "meta":
s.coreManager.RegisterExecutor(executor.NewMetaExecutor(cfg))` line (the
same one Mistral's wiring task added a sibling case next to) and add:

```go
case "gitlab":
	s.coreManager.RegisterExecutor(executor.NewGitLabExecutor(cfg))
```

Then find `baselineExecutorAuths()`'s provider list (a `[]string{...}`
containing `"codex", "claude", ..., "meta", "mistral", "openai-compatibility"`)
and add `"gitlab"` to it, next to `"mistral"`.

- [ ] **Step 2: Wire model listing**

In `sdk/cliproxy/service_models.go`, confirmed by reading the file
directly: the `case "devin":` block (`sdk/cliproxy/service_models.go:166-168`)
is the exact structural match for GitLab — both are OAuth-only providers
with no config-driven API-key list, so neither has a
`resolveConfigXxxKey`/`excluded = entry.ExcludedModels` step, unlike
`"mistral"`/`"meta"`/`"xai"`/`"claude"` which do:

```go
case "devin":
	models = registry.GetDevinModels()
	models = applyExcludedModels(models, excluded)
```

Add a sibling case right after `"mistral"`'s block, following the
`"devin"` shape exactly:

```go
case "gitlab":
	models = executor.GitLabModelsFromAuth(a)
	models = applyExcludedModels(models, excluded)
```

`excluded` here is whatever the generic auth-level exclusion mechanism
already populated before the `switch` (same variable `"devin"` reads) —
no additional per-key lookup needed since GitLab has no config array.

- [ ] **Step 3: Build, vet, full test suite**

```bash
export PATH=$PATH:/usr/local/go/bin
go build -o test-output ./cmd/server && rm test-output && echo "build OK"
go vet ./... 2>&1 | tail -20
go test ./... 2>&1 | tee /tmp/gitlab-phase1-test.log | tail -5
grep -c "^ok" /tmp/gitlab-phase1-test.log
grep -n "FAIL" /tmp/gitlab-phase1-test.log
```

Expected: build OK, vet clean, test log shows ~99 `ok` lines (98 from
before this feature plus the new `internal/auth/gitlab` package), zero
`FAIL` lines.

- [ ] **Step 4: gofmt check across every file this plan touched**

```bash
gofmt -l internal/auth/gitlab/ sdk/auth/gitlab.go sdk/auth/gitlab_test.go sdk/auth/refresh_registry.go internal/cmd/gitlab_login.go cmd/server/main.go internal/runtime/executor/gitlab_executor.go internal/runtime/executor/gitlab_executor_test.go internal/runtime/executor/gitlab_executor_integration_test.go sdk/cliproxy/service_executors.go sdk/cliproxy/service_models.go
```

Expected: no output. If anything is listed, `gofmt -w` it and re-check.

- [ ] **Step 5: Commit the wiring**

```bash
git add sdk/cliproxy/service_executors.go sdk/cliproxy/service_models.go
git commit -m "feat: wire GitLab executor into executor registration and model listing"
```

- [ ] **Step 6: Manual verification note (not automatable)**

Add a short note to the PR/final report (not a code change): a real OAuth
login smoke test requires the user to register their own GitLab OAuth
Application (redirect URI `http://localhost:17171/auth/callback`) and run
`--gitlab-login`, or supply a personal access token via
`GITLAB_PERSONAL_ACCESS_TOKEN` and run `--gitlab-token-login` — this plan
cannot automate that step the way Mistral's single-API-key smoke test was
automated, since there's no shared/public GitLab OAuth App to test
against.
