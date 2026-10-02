package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
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
			"auth_method":           "pat",
			"base_url":              server.URL,
			"personal_access_token": "pat-token-123",
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
