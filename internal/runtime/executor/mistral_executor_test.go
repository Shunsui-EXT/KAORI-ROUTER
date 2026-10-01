package executor

import (
	"testing"

	"github.com/tidwall/gjson"

	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
)

func TestMistralBaseURL_StripsTrailingV1(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": "https://api.mistral.ai/v1"}}

	if got := mistralBaseURL(auth); got != "https://api.mistral.ai" {
		t.Errorf("expected trailing /v1 to be stripped, got %q", got)
	}
}

func TestMistralBaseURL_StripsTrailingSlash(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": "https://api.mistral.ai/"}}

	if got := mistralBaseURL(auth); got != "https://api.mistral.ai" {
		t.Errorf("expected trailing slash to be stripped, got %q", got)
	}
}

func TestMistralBaseURL_FallsBackToDefaultWhenUnset(t *testing.T) {
	if got := mistralBaseURL(&cliproxyauth.Auth{}); got != mistralDefaultBaseURL {
		t.Errorf("expected default base URL for an auth with no base_url attribute, got %q", got)
	}
	if got := mistralBaseURL(nil); got != mistralDefaultBaseURL {
		t.Errorf("expected default base URL for a nil auth, got %q", got)
	}
}

func TestStripMistralUnsupportedFields_RemovesTopLevelFields(t *testing.T) {
	input := []byte(`{"model":"mistral-large-latest","reasoning":{"effort":"high"},"reasoningSummary":"x","include":["x"],"verbosity":"low","interleaved":true,"thinking":{"type":"enabled"},"stream_options":{"include_usage":true},"messages":[]}`)

	out := stripMistralUnsupportedFields(input)

	for _, path := range []string{"reasoning", "reasoningSummary", "include", "verbosity", "interleaved", "thinking", "stream_options"} {
		if gjson.GetBytes(out, path).Exists() {
			t.Errorf("expected %q to be removed, still present in: %s", path, out)
		}
	}
	if !gjson.GetBytes(out, "model").Exists() {
		t.Errorf("expected unrelated field %q to survive, got: %s", "model", out)
	}
}

func TestStripMistralUnsupportedFields_RemovesAssistantReasoningContent(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello","reasoning_content":"because..."}]}`)

	out := stripMistralUnsupportedFields(input)

	msgs := gjson.GetBytes(out, "messages").Array()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages to survive, got %d: %s", len(msgs), out)
	}
	if msgs[1].Get("reasoning_content").Exists() {
		t.Errorf("expected assistant reasoning_content to be removed, got: %s", out)
	}
	if msgs[1].Get("content").String() != "hello" {
		t.Errorf("expected assistant content to survive untouched, got: %s", out)
	}
}

func TestStripMistralUnsupportedFields_DropsEmptyAssistantMessagesWithoutToolCalls(t *testing.T) {
	input := []byte(`{"messages":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":""},` +
		`{"role":"assistant","content":"  "},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"c1"}]},` +
		`{"role":"assistant","content":"real reply"}` +
		`]}`)

	out := stripMistralUnsupportedFields(input)

	msgs := gjson.GetBytes(out, "messages").Array()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 surviving messages (user, tool-call assistant, real-reply assistant), got %d: %s", len(msgs), out)
	}
	if msgs[0].Get("role").String() != "user" {
		t.Errorf("expected first surviving message to be the user message, got: %s", out)
	}
	if !msgs[1].Get("tool_calls").Exists() {
		t.Errorf("expected the tool-call assistant message to survive, got: %s", out)
	}
	if msgs[2].Get("content").String() != "real reply" {
		t.Errorf("expected the real-reply assistant message to survive, got: %s", out)
	}
}

func TestStripMistralUnsupportedFields_NoMessagesField(t *testing.T) {
	input := []byte(`{"model":"mistral-large-latest","reasoning":{"effort":"high"}}`)

	out := stripMistralUnsupportedFields(input)

	if gjson.GetBytes(out, "reasoning").Exists() {
		t.Errorf("expected reasoning to be removed even without a messages field, got: %s", out)
	}
}

func TestNormalizeMistralReasoningEffort_ForcesHighForMistralModel(t *testing.T) {
	input := []byte(`{"model":"mistral-large-latest","reasoning_effort":"low"}`)

	out := normalizeMistralReasoningEffort("mistral-large-latest", input)

	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "high" {
		t.Errorf("expected reasoning_effort to be forced to %q, got %q", "high", got)
	}
}

func TestNormalizeMistralReasoningEffort_LeavesAlreadyHighUntouched(t *testing.T) {
	input := []byte(`{"model":"mistral-large-latest","reasoning_effort":"high"}`)

	out := normalizeMistralReasoningEffort("mistral-large-latest", input)

	if string(out) != string(input) {
		t.Errorf("expected payload to be left untouched when already %q, got: %s", "high", out)
	}
}

func TestNormalizeMistralReasoningEffort_NoReasoningEffortField(t *testing.T) {
	input := []byte(`{"model":"mistral-large-latest"}`)

	out := normalizeMistralReasoningEffort("mistral-large-latest", input)

	if gjson.GetBytes(out, "reasoning_effort").Exists() {
		t.Errorf("expected no reasoning_effort field to be added when absent, got: %s", out)
	}
}

func TestNormalizeMistralReasoningEffort_IgnoresNonMistralModel(t *testing.T) {
	input := []byte(`{"model":"gpt-5","reasoning_effort":"low"}`)

	out := normalizeMistralReasoningEffort("gpt-5", input)

	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "low" {
		t.Errorf("expected reasoning_effort to be left as %q for a non-Mistral model, got %q", "low", got)
	}
}

func TestNormalizeMistralReasoningEffort_CoversMagistralFamily(t *testing.T) {
	for _, model := range []string{"magistral-medium-latest", "magistral-small-latest"} {
		input := []byte(`{"reasoning_effort":"low"}`)

		out := normalizeMistralReasoningEffort(model, input)

		if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "high" {
			t.Errorf("model %q: expected reasoning_effort to be forced to %q, got %q", model, "high", got)
		}
	}
}

func TestNormalizeMistralReasoningEffort_CoversMinistralFamily(t *testing.T) {
	input := []byte(`{"reasoning_effort":"low"}`)

	out := normalizeMistralReasoningEffort("ministral-8b-latest", input)

	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "high" {
		t.Errorf("expected reasoning_effort to be forced to %q, got %q", "high", got)
	}
}
