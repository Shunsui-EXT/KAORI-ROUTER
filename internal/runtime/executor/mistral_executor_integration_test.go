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

// TestMistralExecutor_Execute_Success exercises the full non-streaming success
// path end-to-end against a mock Mistral server: verifies the request lands on
// exactly "/v1/chat/completions" (not doubled, even when base_url already ends
// in "/v1" — the regression this guards is the one found in code review), that
// unsupported fields are stripped before the request leaves the executor, and
// that a real success response is correctly translated back to the client.
func TestMistralExecutor_Execute_Success(t *testing.T) {
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected request path %q, got %q (base-url + endpoint doubled?)", "/v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-mistral-key" {
			t.Errorf("expected Authorization header %q, got %q", "Bearer test-mistral-key", got)
		}
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		requestBody = buf

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_mistral_1","object":"chat.completion","model":"mistral-small-latest","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer server.Close()

	exec := NewMistralExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-mistral-key",
			"base_url": server.URL + "/v1", // deliberately includes /v1, the regression this test guards
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "mistral-small-latest",
		Payload: []byte(`{"model":"mistral-small-latest","messages":[{"role":"user","content":"ping"}],"reasoning":{"effort":"high"}}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	resp, err := exec.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gjson.GetBytes(requestBody, "reasoning").Exists() {
		t.Errorf("expected 'reasoning' field to be stripped before sending, request body: %s", requestBody)
	}

	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "pong" {
		t.Errorf("expected translated response content %q, got %q (payload: %s)", "pong", got, resp.Payload)
	}
}

// TestMistralExecutor_ExecuteStream_Success exercises the full streaming
// success path end-to-end against a mock SSE Mistral server.
func TestMistralExecutor_ExecuteStream_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected request path %q, got %q", "/v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"chatcmpl_mistral_2","object":"chat.completion.chunk","model":"mistral-small-latest","choices":[{"index":0,"delta":{"role":"assistant","content":"pong"},"finish_reason":null}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	exec := NewMistralExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-mistral-key",
			"base_url": server.URL, // no trailing /v1 this time, covers the other branch
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "mistral-small-latest",
		Payload: []byte(`{"model":"mistral-small-latest","messages":[{"role":"user","content":"ping"}],"stream":true}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	result, err := exec.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var combined strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		combined.Write(chunk.Payload)
	}

	if !strings.Contains(combined.String(), "pong") {
		t.Errorf("expected streamed content to contain %q, got: %s", "pong", combined.String())
	}
}

// TestMistralExecutor_Execute_RateLimitError verifies a real 429 response from
// Mistral (the only response this executor was exercised against with a live
// API key during development, due to the test credential's own account having
// zero req/minute quota) is correctly surfaced as a statusErr.
func TestMistralExecutor_Execute_RateLimitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"object":"error","message":"Rate limit exceeded","type":"rate_limited","code":"1300"}`))
	}))
	defer server.Close()

	exec := NewMistralExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{"api_key": "test-mistral-key", "base_url": server.URL},
	}
	req := cliproxyexecutor.Request{
		Model:   "mistral-small-latest",
		Payload: []byte(`{"model":"mistral-small-latest","messages":[{"role":"user","content":"ping"}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")}

	_, err := exec.Execute(context.Background(), auth, req, opts)
	if err == nil {
		t.Fatal("expected an error for a 429 response")
	}
	se, ok := err.(statusErr)
	if !ok {
		t.Fatalf("expected statusErr, got %T: %v", err, err)
	}
	if se.StatusCode() != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", se.StatusCode())
	}
}
