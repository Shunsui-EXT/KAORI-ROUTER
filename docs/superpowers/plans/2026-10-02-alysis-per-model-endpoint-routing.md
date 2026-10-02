# Alysis Per-Model Endpoint Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make all 4 models in the Alysis Code Pro catalog (`deepseek-flash`, `glm-5.3-flash`, `gpt-6-luna`, `claude-sonnet-5-5`) work end-to-end through KAORI ROUTER by routing each model's request to the Alysis gateway endpoint and wire format it actually requires, instead of always calling `/chat/completions`.

**Architecture:** `AlysisExecutor.Execute`/`ExecuteStream` pick a `(sdktranslator.Format, endpoint path)` pair based on the requested model's name prefix via a new `alysisTargetForModel` helper, then reuse the exact same request-building/translation/response-parsing pipeline that already exists — just parameterized by that pair instead of two hardcoded literals. No delegation to other executors; no new translation code; `AlysisExecutor` keeps full control of its own HTTP request construction, headers, and auth.

**Tech Stack:** Go 1.26, this repo's existing `sdk/translator` format system (`sdktranslator.FormatOpenAI`/`FormatOpenAIResponse`/`FormatClaude`), `helps.ParseOpenAIUsage`/`ParseClaudeUsage` usage parsers, `net/http/httptest` for integration tests.

**Spec:** `docs/superpowers/specs/2026-10-02-alysis-per-model-endpoint-routing-design.md`

## Global Constraints

- Only touch `internal/runtime/executor/alysis_executor.go` and its two
  existing test files (`alysis_executor_test.go`,
  `alysis_executor_integration_test.go`). No changes to Alysis's auth,
  login, CLI/management-API wiring, model listing, or TUI entry — those
  are already implemented, reviewed, and merged into this branch's
  history.
- No delegation to `ClaudeExecutor`/`CodexExecutor` (rejected approach,
  per the spec) — `AlysisExecutor` must keep building and sending every
  HTTP request itself.
- Use the exported constants `sdktranslator.FormatOpenAI`,
  `sdktranslator.FormatOpenAIResponse`, `sdktranslator.FormatClaude`
  (confirmed present in `sdk/translator/formats.go`) — not
  `sdktranslator.FromString("openai")`-style string literals.
- Usage parsing: `helps.ParseOpenAIUsage`/`helps.ParseOpenAIStreamUsage`
  for BOTH the `FormatOpenAI` and `FormatOpenAIResponse` targets (these
  functions already handle both the `prompt_tokens`/`completion_tokens`
  chat-completions field names and the `input_tokens`/`output_tokens`
  Responses-API field names — confirmed by reading
  `hasOpenAIStyleUsageBucketFields` in
  `internal/runtime/executor/helps/usage_helpers.go`, which checks all
  four). Do NOT use `helps.ParseCodexUsage` — that function reads a
  `response.usage`-nested shape that is an artifact of Codex's own
  upstream always being SSE, not a generic Responses-API shape. Use
  `helps.ParseClaudeUsage`/`helps.ParseClaudeStreamUsage` only for the
  `FormatClaude` target.
- No automatic retry or fallback across endpoints if the routing
  heuristic is ever wrong for a given model — the real upstream error
  (already proven to be clear and specific, e.g. `"Luna uses /v1/responses
  for reasoning and tools."`) must still surface as a `statusErr` with
  the real status code and body, exactly as it does today.
- Every touched file must pass `gofmt -l` with no output, `go vet
  ./...` clean, `go build ./...` clean.
- **Mandatory live verification before merge (continuing the standing
  requirement for this provider):** after this plan's tasks are
  implemented and reviewed clean, the user will run real chat
  completions (streaming and non-streaming) against `gpt-6-luna` and
  `claude-sonnet-5-5` through the live KAORI ROUTER server, against the
  real Alysis gateway — continuing from the already-successful live
  verification of `deepseek-flash`/`glm-5.3-flash` done earlier, not
  starting over. Only after that confirmation does the branch become
  mergeable.

## Review Focus

- A model name that does **not** start with `gpt-` or `claude-` (e.g.
  `deepseek-flash`, `glm-5.3-flash`, or any future/unknown model) must
  still route to `/chat/completions` with `FormatOpenAI` — the existing,
  already-proven-working default. Adding the two new branches must not
  regress this.
- A model name with different casing or surrounding whitespace (e.g.
  `"Claude-Sonnet-5-5"` or `" gpt-6-luna "`) must still classify
  correctly — the heuristic lowercases and trims before matching.
- The response for a Claude-routed (or Responses-routed) request must
  translate back to whatever format the **client** originally
  requested, not just come back in Alysis's own wire shape — a bug in
  the `to`→`from` direction of `TranslateNonStream`/`TranslateStream`
  would silently return Claude-native or Responses-native JSON to an
  OpenAI-expecting client instead of translating it.
- The missing-gateway-key check must still short-circuit with the
  existing `statusErr{401, "alysis: missing gateway key"}` **before**
  any model-based routing logic runs, regardless of which model was
  requested — this ordering must not accidentally move after
  model-specific logic.
- The new integration tests must assert the mock server actually
  received the request on the **correct path** (`/responses` or
  `/messages`) with the correctly-translated body shape — not merely
  that some request happened and got a 200, which would also pass if
  routing were silently broken and everything still hit
  `/chat/completions`.

---

### Task 1: `alysisTargetForModel` routing helper

**Files:**
- Modify: `internal/runtime/executor/alysis_executor.go`
- Test: `internal/runtime/executor/alysis_executor_test.go`

**Interfaces:**
- Produces: `alysisTargetForModel(baseModel string) (sdktranslator.Format,
  string)` — returns the translator format to use as `to` and the
  gateway endpoint path (e.g. `"/chat/completions"`, `"/responses"`,
  `"/messages"`) for a given base model name. Task 2 consumes this
  directly from `Execute` and `ExecuteStream`.

- [ ] **Step 1: Write the failing unit tests**

Add to `internal/runtime/executor/alysis_executor_test.go` (the existing
file already has `package executor` and imports `testing` and
`cliproxyauth` — add `sdktranslator
"github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"` to its import
block):

```go
func TestAlysisTargetForModel(t *testing.T) {
	cases := []struct {
		model        string
		wantFormat   sdktranslator.Format
		wantEndpoint string
	}{
		{"deepseek-flash", sdktranslator.FormatOpenAI, "/chat/completions"},
		{"glm-5.3-flash", sdktranslator.FormatOpenAI, "/chat/completions"},
		{"gpt-6-luna", sdktranslator.FormatOpenAIResponse, "/responses"},
		{"claude-sonnet-5-5", sdktranslator.FormatClaude, "/messages"},
		{"some-future-model", sdktranslator.FormatOpenAI, "/chat/completions"},
		{"Claude-Sonnet-5-5", sdktranslator.FormatClaude, "/messages"},
		{" gpt-6-luna ", sdktranslator.FormatOpenAIResponse, "/responses"},
		{"", sdktranslator.FormatOpenAI, "/chat/completions"},
	}
	for _, c := range cases {
		gotFormat, gotEndpoint := alysisTargetForModel(c.model)
		if gotFormat != c.wantFormat || gotEndpoint != c.wantEndpoint {
			t.Errorf("alysisTargetForModel(%q) = (%q, %q), want (%q, %q)",
				c.model, gotFormat, gotEndpoint, c.wantFormat, c.wantEndpoint)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runtime/executor/... -run TestAlysisTargetForModel -v`
Expected: FAIL — `undefined: alysisTargetForModel` (the function doesn't
exist yet).

- [ ] **Step 3: Implement the helper**

Add this function to `internal/runtime/executor/alysis_executor.go`,
right after the `chatCompletionsURL` method (Task 2 removes that method
and this function replaces its routing role):

```go
// alysisTargetForModel picks the translator format and gateway endpoint
// path for a given model. Alysis's hosted gateway serves different model
// families through different wire formats: DeepSeek/GLM models accept
// plain OpenAI chat-completions, GPT-family models require the OpenAI
// Responses API shape at /responses, and Claude-family models require
// the Anthropic Messages API shape at /messages (confirmed via live
// 400 responses naming the correct endpoint for each). Any unrecognized
// model name falls back to the chat-completions default.
func alysisTargetForModel(baseModel string) (sdktranslator.Format, string) {
	model := strings.ToLower(strings.TrimSpace(baseModel))
	switch {
	case strings.HasPrefix(model, "claude-"):
		return sdktranslator.FormatClaude, "/messages"
	case strings.HasPrefix(model, "gpt-"):
		return sdktranslator.FormatOpenAIResponse, "/responses"
	default:
		return sdktranslator.FormatOpenAI, "/chat/completions"
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/runtime/executor/... -run TestAlysisTargetForModel -v`
Expected: PASS.

- [ ] **Step 5: gofmt, build, commit**

```bash
gofmt -w internal/runtime/executor/alysis_executor.go internal/runtime/executor/alysis_executor_test.go
go build ./internal/runtime/executor/... && echo "build OK"
git add internal/runtime/executor/alysis_executor.go internal/runtime/executor/alysis_executor_test.go
git commit -m "feat: add alysisTargetForModel per-model endpoint routing helper"
```

---

### Task 2: Wire routing into Execute/ExecuteStream, fix usage parsing, integration tests

**Files:**
- Modify: `internal/runtime/executor/alysis_executor.go`
- Test: `internal/runtime/executor/alysis_executor_test.go`
- Test: `internal/runtime/executor/alysis_executor_integration_test.go`

**Interfaces:**
- Consumes: Task 1's `alysisTargetForModel(baseModel string)
  (sdktranslator.Format, string)`.
- Modifies the behavior of `AlysisExecutor.Execute` and
  `AlysisExecutor.ExecuteStream` (same exported signatures, unchanged) —
  no new exported symbols.

- [ ] **Step 1: Remove `chatCompletionsURL` and add a generic `gatewayURL` helper**

Replace this method in `internal/runtime/executor/alysis_executor.go`:

```go
func (e *AlysisExecutor) chatCompletionsURL() string {
	return alysisGatewayBase + "/chat/completions"
}
```

with:

```go
func (e *AlysisExecutor) gatewayURL(endpoint string) string {
	return alysisGatewayBase + endpoint
}
```

- [ ] **Step 2: Update the existing `TestAlysisChatCompletionsURL` test**

The existing test in `internal/runtime/executor/alysis_executor_test.go`
calls the now-removed `e.chatCompletionsURL()`. Replace it:

```go
func TestAlysisGatewayURL(t *testing.T) {
	e := NewAlysisExecutor(nil)
	got := e.gatewayURL("/chat/completions")
	want := "https://vzigujbcjjmpntxhmyvr.supabase.co/functions/v1/llm/v1/chat/completions"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 3: Write the failing integration tests for the two new routing branches**

Add to `internal/runtime/executor/alysis_executor_integration_test.go`
(the existing file already imports `context`, `net/http`,
`net/http/httptest`, `strings`, `testing`, `config`, the blank
`internal/translator` import, `cliproxyauth`, `cliproxyexecutor`,
`sdktranslator` — reuse all of these, no new imports needed beyond what's
already there):

```go
func TestAlysisExecutor_Execute_RoutesGPTModelsToResponsesEndpoint(t *testing.T) {
	var sawPath string
	var sawAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"luna-pong"}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}`))
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
	if sawAuthHeader != "Bearer slk_luna_token" {
		t.Errorf("expected Authorization %q, got %q", "Bearer slk_luna_token", sawAuthHeader)
	}
	if !strings.Contains(string(resp.Payload), "luna-pong") {
		t.Errorf("expected translated response to contain %q, got: %s", "luna-pong", resp.Payload)
	}
}

func TestAlysisExecutor_Execute_RoutesClaudeModelsToMessagesEndpoint(t *testing.T) {
	var sawPath string
	var sawAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuthHeader = r.Header.Get("Authorization")
		if sawAuthHeader == "" {
			sawAuthHeader = r.Header.Get("x-api-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"text","text":"sonnet-pong"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`))
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
	if sawAuthHeader != "Bearer slk_sonnet_token" {
		t.Errorf("expected auth header to carry %q, got %q", "Bearer slk_sonnet_token", sawAuthHeader)
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
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"luna-stream-pong\"}]},\"output_index\":0}\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":1,\"total_tokens\":6}}}\n\n"))
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
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/runtime/executor/... -run "TestAlysisExecutor_(Execute|ExecuteStream)_Routes" -v`
Expected: FAIL — the mock servers will see requests still landing on
`/chat/completions` (today's hardcoded path), so `sawPath` won't match
`/responses`/`/messages`, and/or the response translation will fail
since the mock bodies are Responses-API/Claude-Messages-shaped while the
code still treats them as plain OpenAI chat-completions.

- [ ] **Step 5: Update `Execute` to use the routing helper**

Replace the current `Execute` method in
`internal/runtime/executor/alysis_executor.go` with:

```go
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

	to, endpoint := alysisTargetForModel(baseModel)
	from := opts.SourceFormat
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), false)

	url := e.gatewayURL(endpoint)
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
	if to == sdktranslator.FormatClaude {
		reporter.Publish(ctx, helps.ParseClaudeUsage(body))
	} else {
		reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
	}
	reporter.EnsurePublished(ctx)

	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, body, &param)
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}
```

- [ ] **Step 6: Update `ExecuteStream` to use the routing helper**

Replace the current `ExecuteStream` method with:

```go
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

	to, endpoint := alysisTargetForModel(baseModel)
	from := opts.SourceFormat
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), true)

	url := e.gatewayURL(endpoint)
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
			if to == sdktranslator.FormatClaude {
				if detail, ok := helps.ParseClaudeStreamUsage(trimmed); ok {
					reporter.Publish(ctx, detail)
				}
			} else {
				if detail, ok := helps.ParseOpenAIStreamUsage(trimmed); ok {
					reporter.Publish(ctx, detail)
				}
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
```

Note what changed from the original in both methods: `to` is now
`alysisTargetForModel(baseModel)`'s first return value instead of the
hardcoded `sdktranslator.FromString("openai")`; `url` now uses
`e.gatewayURL(endpoint)` with the routed `endpoint` instead of
`e.chatCompletionsURL()`; the usage-parsing call branches on `to ==
sdktranslator.FormatClaude` to pick `ParseClaudeUsage`/
`ParseClaudeStreamUsage` instead of the OpenAI parsers. Everything else —
header construction, request logging, HTTP client, status-range error
handling, response-body reading, the streaming scanner loop structure —
is byte-for-byte unchanged from today.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/runtime/executor/... -run "TestAlysis" -v`
Expected: PASS — all unit tests (including Task 1's and the updated
`TestAlysisGatewayURL`) and all 4 new integration tests, plus the
pre-existing `TestAlysisExecutor_Execute_Success`,
`_MissingGatewayKey`, `_UpstreamErrorSurfaced`,
`TestFetchAlysisModels_*` tests (which exercise `deepseek-v4-flash`-
style request payloads through the unchanged default branch — these
must still pass unmodified, proving the default path didn't regress).

- [ ] **Step 8: gofmt, build, full package test, commit**

```bash
gofmt -w internal/runtime/executor/alysis_executor.go internal/runtime/executor/alysis_executor_test.go internal/runtime/executor/alysis_executor_integration_test.go
go build ./... && echo "build OK"
go vet ./internal/runtime/executor/... && echo "vet OK"
go test ./internal/runtime/executor/... -v 2>&1 | tail -60
git add internal/runtime/executor/alysis_executor.go internal/runtime/executor/alysis_executor_test.go internal/runtime/executor/alysis_executor_integration_test.go
git commit -m "feat: route Alysis requests to the correct per-model endpoint and wire format"
```

- [ ] **Step 9: Full-repo verification and mandatory manual live-verification note**

```bash
go test ./... 2>&1 | tee /tmp/alysis-routing-test.log | tail -5
grep -c "^ok" /tmp/alysis-routing-test.log
grep -n "FAIL" /tmp/alysis-routing-test.log
rm -f /tmp/alysis-routing-test.log
```

Expected: no `FAIL` lines, `ok` count matches the pre-existing baseline
(this task adds no new Go packages, only tests within the existing
`internal/runtime/executor` package).

This plan's automation ends here. Before this branch merges to `main`,
the user runs real chat completions (both streaming and non-streaming)
against `gpt-6-luna` and `claude-sonnet-5-5` through the live KAORI
ROUTER server, against the real Alysis gateway — continuing from the
already-successful live verification of `deepseek-flash`/`glm-5.3-flash`
done earlier in this provider's rollout. This also serves as the final,
empirical confirmation that `helps.ParseOpenAIUsage` correctly extracts
token counts from Alysis's actual `/responses` response shape (the one
specific detail this plan's reasoning, backed by reading
`hasOpenAIStyleUsageBucketFields`'s source, could not verify without a
live call). Only after the user confirms this succeeds does the branch
become mergeable — this is a human decision gate, not a step any agent
should perform or claim as complete on the user's behalf.
