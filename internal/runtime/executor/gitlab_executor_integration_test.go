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
	opts := cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FromString("openai"),
		ResponseFormat: sdktranslator.FormatClaude,
	}

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
