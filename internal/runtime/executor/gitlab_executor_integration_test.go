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
	"github.com/tidwall/gjson"
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
	if sawAuthHeader != "Bearer gw-token-abc" {
		t.Errorf("expected the delegated Claude executor to send the swapped-in gateway token, got %q", sawAuthHeader)
	}
	if !strings.Contains(string(resp.Payload), "pong") {
		t.Errorf("expected translated response to contain %q, got: %s", "pong", resp.Payload)
	}
}

// TestGitLabExecutor_Execute_DelegatesToCodexForOpenAIManagedModel mirrors the
// Claude delegation test above, but for an OpenAI-managed account: GitLab
// direct_access metadata says "openai", so buildGitLabOpenAIGatewayAuth must
// fire and delegate to the Codex executor with the swapped gateway
// credentials. Codex always expects an SSE response from its upstream, even
// for a non-streaming Execute call.
func TestGitLabExecutor_Execute_DelegatesToCodexForOpenAIManagedModel(t *testing.T) {
	var sawAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},"output_index":0}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	exec := NewGitLabExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "gitlab",
		Metadata: map[string]any{
			"model_provider":       "openai",
			"model_name":           "gpt-5-codex",
			"duo_gateway_base_url": server.URL,
			"duo_gateway_token":    "gw-token-def",
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gitlab-duo",
		Payload: []byte(`{"model":"gitlab-duo","messages":[{"role":"user","content":"ping"}]}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
	}

	resp, err := exec.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawAuthHeader != "Bearer gw-token-def" {
		t.Errorf("expected the delegated Codex executor to send the swapped-in gateway token, got %q", sawAuthHeader)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "ok" {
		t.Errorf("expected translated response content %q, got %q; payload=%s", "ok", got, resp.Payload)
	}
}

// TestGitLabExecutor_ExecuteStream_DelegatesToClaudeForAnthropicManagedModel
// drives the GitLab-to-Claude native-gateway path through ExecuteStream
// rather than Execute, proving the swapped gateway credentials reach a real
// mocked upstream and the streamed response round-trips correctly.
func TestGitLabExecutor_ExecuteStream_DelegatesToClaudeForAnthropicManagedModel(t *testing.T) {
	const streamData = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	var sawAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("x-api-key")
		if sawAuthHeader == "" {
			sawAuthHeader = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = w.Write([]byte(streamData))
			flusher.Flush()
		}
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

	result, err := exec.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		output.Write(chunk.Payload)
	}

	if sawAuthHeader != "Bearer gw-token-abc" {
		t.Errorf("expected the delegated Claude executor to send the swapped-in gateway token, got %q", sawAuthHeader)
	}
	if !strings.Contains(output.String(), "pong") {
		t.Errorf("expected streamed output to contain %q, got: %s", "pong", output.String())
	}
}

func TestGitLabModelsFromAuth_IncludesStableAliasAndDiscoveredModel(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{
			"model_provider": "anthropic",
			"model_name":     "claude-opus-4-7-preview",
		},
	}
	models := GitLabModelsFromAuth(auth)

	var sawStableAlias, sawDiscovered bool
	for _, m := range models {
		switch m.ID {
		case "gitlab-duo":
			sawStableAlias = true
		case "claude-opus-4-7-preview":
			sawDiscovered = true
		}
	}
	if !sawStableAlias {
		t.Error("expected the stable 'gitlab-duo' alias to be present")
	}
	if !sawDiscovered {
		t.Error("expected the dynamically discovered model to be present")
	}
}
