package executor

import (
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
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/util"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

const (
	gitLabProviderKey     = "gitlab"
	gitLabAuthMethodOAuth = "oauth"
	gitLabAuthMethodPAT   = "pat"
)

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
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
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

	var oauthRefreshErr error
	if method == gitLabAuthMethodOAuth {
		if refreshed, refreshErr := e.refreshOAuthToken(ctx, client, auth, baseURL); refreshErr != nil {
			oauthRefreshErr = logGitLabOAuthRefreshErr(refreshErr)
		} else if refreshed != nil {
			token = refreshed.AccessToken
			applyGitLabTokenMetadata(auth.Metadata, refreshed)
		}
	}

	direct, err := client.FetchDirectAccess(ctx, baseURL, token)
	if err != nil && method == gitLabAuthMethodOAuth {
		if refreshed, refreshErr := e.refreshOAuthToken(ctx, client, auth, baseURL); refreshErr != nil {
			oauthRefreshErr = logGitLabOAuthRefreshErr(refreshErr)
		} else if refreshed != nil {
			token = refreshed.AccessToken
			applyGitLabTokenMetadata(auth.Metadata, refreshed)
			direct, err = client.FetchDirectAccess(ctx, baseURL, token)
		}
	}
	if err != nil {
		if oauthRefreshErr != nil {
			return nil, fmt.Errorf("gitlab duo executor: oauth token refresh failed: %w (direct_access also failed: %v)", oauthRefreshErr, err)
		}
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
	gitlab.MergeDirectAccessMetadata(auth.Metadata, direct)
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

// logGitLabOAuthRefreshErr logs an OAuth refresh failure and returns it
// unless it is the benign "refresh token missing" case (PAT-derived auths,
// or auths that have not done an OAuth login, simply have no refresh token).
// Any other failure (e.g. a revoked grant) is logged at Warn and returned so
// the caller can propagate it when the fallback direct_access call also fails.
func logGitLabOAuthRefreshErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "refresh token missing") {
		log.WithField("provider", "gitlab").Debugf("gitlab oauth token refresh skipped: %v", err)
		return nil
	}
	log.WithField("provider", "gitlab").Warnf("gitlab oauth token refresh failed: %v", err)
	return err
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

func gitLabAuthKind(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case gitLabAuthMethodPAT:
		return "personal_access_token"
	default:
		return "oauth"
	}
}

// GitLabModelsFromAuth lists the stable "gitlab-duo" alias and any model
// GitLab's direct_access metadata currently reports for this specific
// account. There is no hardcoded catalog: GitLab Duo's own model IDs are
// not valid Anthropic/OpenAI model IDs, so advertising them here would
// make them selectable (and routable) without a working upstream mapping.
func GitLabModelsFromAuth(auth *cliproxyauth.Auth) []*registry.ModelInfo {
	models := make([]*registry.ModelInfo, 0, 4)
	seen := make(map[string]struct{}, 4)
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
