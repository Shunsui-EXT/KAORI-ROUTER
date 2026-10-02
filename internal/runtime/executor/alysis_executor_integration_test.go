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
