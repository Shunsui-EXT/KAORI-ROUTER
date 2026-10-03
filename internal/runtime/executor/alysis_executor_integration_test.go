package executor

import (
	"context"
	"io"
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

func TestAlysisExecutor_Execute_RoutesGPTModelsToResponsesEndpoint(t *testing.T) {
	var sawPath string
	var sawAuthHeader string
	var sawStreamField bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuthHeader = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		sawStreamField = gjson.GetBytes(bodyBytes, "stream").Bool()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"luna-pong"}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}` + "\n\n"))
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	exec := NewAlysisExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_luna_token"}}
	req := cliproxyexecutor.Request{
		Model:   "gpt-6-luna",
		Payload: []byte(`{"model":"gpt-6-luna","messages":[{"role":"user","content":"ping"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	resp, err := exec.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawPath != "/responses" {
		t.Errorf("expected request path %q, got %q", "/responses", sawPath)
	}
	if !sawStreamField {
		t.Errorf("expected the upstream request to set stream:true for the Codex-routed gpt-6-luna path")
	}
	if sawAuthHeader != "Bearer slk_luna_token" {
		t.Errorf("expected Authorization %q, got %q", "Bearer slk_luna_token", sawAuthHeader)
	}
	if !strings.Contains(string(resp.Payload), `"object":"chat.completion"`) {
		t.Errorf("expected a translated chat.completion object, got: %s", resp.Payload)
	}
	if !strings.Contains(string(resp.Payload), "luna-pong") {
		t.Errorf("expected translated response to contain %q, got: %s", "luna-pong", resp.Payload)
	}
}

func TestAlysisExecutor_Execute_RoutesClaudeModelsToMessagesEndpoint(t *testing.T) {
	const streamData = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-5-5","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"sonnet-pong"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	var sawPath string
	var sawAuthHeader string
	var sawStreamField bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuthHeader = r.Header.Get("Authorization")
		if sawAuthHeader == "" {
			sawAuthHeader = r.Header.Get("x-api-key")
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		sawStreamField = gjson.GetBytes(bodyBytes, "stream").Bool()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(streamData))
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	exec := NewAlysisExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_sonnet_token"}}
	req := cliproxyexecutor.Request{
		Model:   "claude-sonnet-5-5",
		Payload: []byte(`{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"ping"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	resp, err := exec.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawPath != "/messages" {
		t.Errorf("expected request path %q, got %q", "/messages", sawPath)
	}
	if !sawStreamField {
		t.Errorf("expected the upstream request to set stream:true for the Claude non-stream translation path")
	}
	if sawAuthHeader != "Bearer slk_sonnet_token" {
		t.Errorf("expected auth header to carry %q, got %q", "Bearer slk_sonnet_token", sawAuthHeader)
	}
	if !strings.Contains(string(resp.Payload), `"object":"chat.completion"`) {
		t.Errorf("expected a translated chat.completion object, got: %s", resp.Payload)
	}
	if !strings.Contains(string(resp.Payload), "sonnet-pong") {
		t.Errorf("expected translated response to contain %q, got: %s", "sonnet-pong", resp.Payload)
	}
}

func TestAlysisExecutor_ExecuteStream_RoutesGPTModelsToResponsesEndpoint(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = w.Write([]byte("data: " + `{"type":"response.output_text.delta","delta":"luna-stream-pong"}` + "\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("data: " + `{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}` + "\n\n"))
			flusher.Flush()
		}
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	exec := NewAlysisExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_luna_token"}}
	req := cliproxyexecutor.Request{
		Model:   "gpt-6-luna",
		Payload: []byte(`{"model":"gpt-6-luna","messages":[{"role":"user","content":"ping"}],"stream":true}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	result, err := exec.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var collected strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		collected.Write(chunk.Payload)
	}
	if sawPath != "/responses" {
		t.Errorf("expected request path %q, got %q", "/responses", sawPath)
	}
	if !strings.Contains(collected.String(), "luna-stream-pong") {
		t.Errorf("expected streamed output to contain %q, got: %s", "luna-stream-pong", collected.String())
	}
	if !strings.Contains(collected.String(), `"object":"chat.completion.chunk"`) {
		t.Errorf("expected translated chat.completion.chunk payloads, got: %s", collected.String())
	}
}

func TestAlysisExecutor_ExecuteStream_RoutesClaudeModelsToMessagesEndpoint(t *testing.T) {
	const streamData = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-5-5","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"sonnet-stream-pong"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = w.Write([]byte(streamData))
			flusher.Flush()
		}
	}))
	defer server.Close()

	originalBase := alysisGatewayBase
	alysisGatewayBase = server.URL
	defer func() { alysisGatewayBase = originalBase }()

	exec := NewAlysisExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_sonnet_token"}}
	req := cliproxyexecutor.Request{
		Model:   "claude-sonnet-5-5",
		Payload: []byte(`{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"ping"}],"stream":true}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	result, err := exec.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var collected strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		collected.Write(chunk.Payload)
	}
	if sawPath != "/messages" {
		t.Errorf("expected request path %q, got %q", "/messages", sawPath)
	}
	if !strings.Contains(collected.String(), "sonnet-stream-pong") {
		t.Errorf("expected streamed output to contain %q, got: %s", "sonnet-stream-pong", collected.String())
	}
	if !strings.Contains(collected.String(), `"object":"chat.completion.chunk"`) {
		t.Errorf("expected translated chat.completion.chunk payloads, got: %s", collected.String())
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
	if len(models) != 4 {
		t.Fatalf("expected the 4-entry static fallback catalog, got %d entries", len(models))
	}
}

func TestFetchAlysisModels_MergesLiveCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash"},{"id":"deepseek-v5-preview","name":"DeepSeek V5 Preview"}]}`))
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
		if m.ID == "claude-sonnet-5-5" {
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
