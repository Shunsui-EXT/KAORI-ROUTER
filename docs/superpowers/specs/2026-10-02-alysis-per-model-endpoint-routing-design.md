# Alysis Code Pro — Per-Model Endpoint Routing — Design

## Context

The Alysis Code Pro provider (branch `feature/alysis-code-pro-provider`,
not yet merged) was fully implemented, reviewed (task-level + final
whole-branch review + one fix wave), and passed its mandatory live
device-flow verification. During that live verification, chat completions
against all 4 catalog models revealed a new fact: Alysis's hosted gateway
(`https://vzigujbcjjmpntxhmyvr.supabase.co/functions/v1/llm/v1`) does not
serve every model through `/chat/completions`. Two of the four models
returned a clear 400 instead:

- `gpt-6-luna` → `{"error":{"message":"Luna uses /v1/responses for reasoning and tools.", ...}}`
- `claude-sonnet-5-5` → `{"error":{"message":"Sonnet uses /v1/messages for reasoning and tools.", ...}}`

`deepseek-flash` and `glm-5.3-flash` both work correctly through
`/chat/completions` today. This spec covers making the other two models
actually usable, by routing each model's request to the correct
Alysis-side endpoint and wire format.

## Goals

- All 4 models in the Alysis Code Pro catalog (`deepseek-flash`,
  `glm-5.3-flash`, `gpt-6-luna`, `claude-sonnet-5-5`) work correctly
  end-to-end through KAORI ROUTER, for both `Execute` (non-streaming) and
  `ExecuteStream` (streaming).
- No new translation code: reuse this repo's existing
  `sdk/translator` format conversions (`FormatOpenAI`,
  `FormatOpenAIResponse`, `FormatClaude`) exactly as `OpenAICompatExecutor`
  already does for its own `openai`/`openai-response` branch — this is a
  direct, already-proven precedent in this exact codebase
  (`internal/runtime/executor/openai_compat_executor.go`'s `Execute`
  method branches `to`/`endpoint` on `opts.Alt == "responses/compact"`;
  this spec reuses the same mechanic, triggered by model-name prefix
  instead of an explicit client flag).
- `AlysisExecutor` keeps full control of its own HTTP request
  construction, headers, and auth — no delegation to `ClaudeExecutor` or
  `CodexExecutor` (rejected approach; see Decisions).

## Non-goals

- No change to Alysis's auth, login flow, model listing, management-API
  endpoint, or TUI entry (Tasks 1/2/3/5/6 of the original plan are
  untouched).
- No attempt to discover routing dynamically from the gateway (confirmed:
  the `/models` response carries no field indicating which endpoint a
  model needs — this is a static, hardcoded mapping based on the models
  actually observed).
- No support for Alysis models beyond the 4 already in the catalog unless
  they're later added — the mapping function treats any unrecognized
  model name as the `/chat/completions` default, so a future 5th model
  that also needs `/responses` or `/messages` isn't silently broken, but
  isn't automatically routed correctly either until someone updates the
  prefix mapping. This is an accepted, documented limitation given there
  is no dynamic signal to detect it from.

## Decisions

### Rejected approach: delegate to `ClaudeExecutor`/`CodexExecutor`

Considered and rejected: clone the Alysis `Auth`, swap in
`Attributes["api_key"]`/`["base_url"]` pointed at Alysis's gateway, and
hand the whole call to `NewCodexExecutor(cfg)`/`NewClaudeExecutor(cfg)` —
the same "native-gateway delegation" mechanic used for GitLab Duo Phase 1.

Rejected because, unlike GitLab Duo's gateway (a transparent pass-through
to the real Anthropic/OpenAI APIs, which is exactly what `ClaudeExecutor`/
`CodexExecutor` are built to talk to), `CodexExecutor`'s own `Execute`
path for the `/responses`-shaped case is not a generic Responses-API
client — reading `internal/runtime/executor/codex_executor_execute.go`
directly shows the "codex" translation target carries substantial
Codex-platform-specific normalization baked in before the request is
ever sent: reasoning-replay-cache handling, parallel-tool-call
normalization, multi-agent-v2 optimization, Codex-specific instruction
normalization, image-generation tool injection, and Codex routing-hint
headers. None of this is appropriate to send to Alysis's gateway, which
has its own (unknown, third-party) backend behavior. Delegating wholesale
risks silently sending Codex-specific payload shapes or headers Alysis's
gateway doesn't expect, with no way to verify correctness short of trial
and error against the live service repeatedly.

### Chosen approach: reuse the translator layer only

`AlysisExecutor.Execute`/`ExecuteStream` select a `(target format,
endpoint path)` pair via a small new helper function,
`alysisTargetForModel(baseModel string) (sdktranslator.Format, string)`:

```go
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

This mirrors `OpenAICompatExecutor.Execute`'s own existing branch
(`internal/runtime/executor/openai_compat_executor.go` lines ~104-109)
almost exactly in shape — the only difference is the trigger condition
(model-name prefix here, `opts.Alt` there). Everything else in
`Execute`/`ExecuteStream` — `sdktranslator.TranslateRequest`, building the
`http.NewRequestWithContext`, setting `Content-Type`/`Authorization`
headers, `util.ApplyCustomHeadersFromAttrs`, `helps.RecordAPIRequest`,
`helps.NewProxyAwareHTTPClient`, status-range error handling,
`sdktranslator.TranslateNonStream`/`TranslateStream` for the response —
stays exactly the same shape it is today, just parameterized by the
selected `to` format and `endpoint` instead of the two literals
`sdktranslator.FromString("openai")` and `"/chat/completions"` that are
hardcoded today.

`AlysisExecutor` still builds and sends every HTTP request itself with
its own headers/URL/auth — nothing about the request's transport layer
is delegated to another executor. Only the JSON *shape* conversion
(OpenAI-chat ↔ OpenAI-Responses ↔ Claude-Messages) is reused from
`sdk/translator`, exactly the layer `OpenAICompatExecutor` already proves
is safe and correct to reuse this way.

### Usage parsing per format

- `FormatOpenAI` (`/chat/completions`): unchanged —
  `helps.ParseOpenAIUsage`/`helps.ParseOpenAIStreamUsage`, exactly as
  today.
- `FormatClaude` (`/messages`): `helps.ParseClaudeUsage`/
  `helps.ParseClaudeStreamUsage` (confirmed present in
  `internal/runtime/executor/helps/usage_helpers.go`).
- `FormatOpenAIResponse` (`/responses`): **not yet confirmed.** This repo
  has a separate `helps.ParseCodexUsage` specifically for the Responses-API
  usage shape as Codex's own backend returns it — whether Alysis's
  `/responses` endpoint returns a usage object close enough to that same
  shape for `ParseCodexUsage` to parse correctly, or close enough to
  `ParseOpenAIUsage`'s shape instead, is unknown until tested against the
  live gateway. The implementation plan must try `ParseCodexUsage` first
  (it's the one this repo already associates with the `openai-response`
  format in `internal/runtime/executor/helps/plugin_executor_usage.go`'s
  own `case "codex", "openai-response":` grouping) and confirm empirically
  during the mandatory live-verification step, not assume either is
  correct without evidence.

### Error handling

If `alysisTargetForModel`'s prefix heuristic is ever wrong for a given
model (e.g. a future model needs `/responses` but doesn't start with
`gpt-`), Alysis's own gateway already demonstrates it returns a clear,
specific 400 naming the correct endpoint (as shown in Context above) —
this error passes through as a `statusErr` with the real upstream status
and body, unchanged from today's behavior. No automatic retry against a
different endpoint is attempted — a wrong guess should fail loudly and
specifically, not silently retry against multiple endpoints and risk
masking the real problem or double-billing credits for a single logical
request.

## Testing / verification plan

- Unit test for `alysisTargetForModel`: table-driven, covering all 4
  known models plus at least one unrecognized model name (confirming the
  `/chat/completions` default).
- httptest integration tests for the two new branches
  (`TestAlysisExecutor_Execute_RoutesGPTModelsToResponsesEndpoint`,
  `TestAlysisExecutor_Execute_RoutesClaudeModelsToMessagesEndpoint`, plus
  streaming equivalents), using standard OpenAI-Responses-API and
  Anthropic-Messages-API mock response shapes already established
  elsewhere in this repo's own tests (e.g. the Responses-API SSE shape
  from `codex_executor_stream_output_test.go`, the Claude Messages SSE
  shape from `claude_executor_stream_terminal_test.go`, both already read
  in full during the original Alysis Phase 1 planning) — asserting the
  mock receives the request at the correct path (`/responses` /
  `/messages`) with the correct translated payload shape, and the
  response translates back to the client's requested format correctly.
- **Mandatory live verification before merge (same standing requirement
  as the rest of this provider):** after implementation and task review
  pass, the user runs real chat completions against `gpt-6-luna` and
  `claude-sonnet-5-5` through the live KAORI ROUTER server (both
  streaming and non-streaming), confirming HTTP 200 and a real,
  sensible response — specifically also confirming which usage-parsing
  function (`ParseCodexUsage` vs `ParseOpenAIUsage`) actually produces
  correct token counts for the `/responses` path, since that's the one
  open empirical question this spec cannot resolve by reading code alone.
  Only after this live confirmation does the branch become mergeable.

## Risks / notes

- The prefix-based mapping (`claude-*`, `gpt-*`) is derived from exactly
  4 observed models, not from any documented Alysis API contract. If
  Alysis ever adds a 5th model whose provider family needs a 3rd/4th
  wire format (e.g. a Gemini-backed model needing yet another shape),
  this mapping will silently misroute it to `/chat/completions` and
  produce the same kind of clear 400 error this spec is fixing for the
  current 2 models — not a silent failure, but a real one, requiring a
  follow-up fix at that time. Accepted as a known limitation given there
  is no dynamic way to detect this today.
- This spec does not touch `CountTokens` — Alysis's executor already
  returns `statusErr{http.StatusNotImplemented, "alysis: count tokens
  not supported"}` for all models, consistent with the provider's
  pre-existing scope. No model-specific routing is needed there since
  nothing is actually being sent upstream for that method.
