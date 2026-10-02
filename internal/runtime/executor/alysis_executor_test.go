package executor

import (
	"testing"

	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	sdktranslator "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"
)

func TestAlysisCredentials_FromMetadata(t *testing.T) {
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"gatewayKey": "slk_abc"}}
	if got := alysisCredentials(auth); got != "slk_abc" {
		t.Errorf("got %q, want %q", got, "slk_abc")
	}
}

func TestAlysisCredentials_FallsBackToAttributes(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"gatewayKey": "slk_fallback"}}
	if got := alysisCredentials(auth); got != "slk_fallback" {
		t.Errorf("got %q, want %q", got, "slk_fallback")
	}
}

func TestAlysisCredentials_EmptyWhenMissing(t *testing.T) {
	if got := alysisCredentials(nil); got != "" {
		t.Errorf("got %q, want empty string for nil auth", got)
	}
	if got := alysisCredentials(&cliproxyauth.Auth{}); got != "" {
		t.Errorf("got %q, want empty string for auth with no credential", got)
	}
}

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

func TestAlysisChatCompletionsURL(t *testing.T) {
	e := NewAlysisExecutor(nil)
	got := e.chatCompletionsURL()
	want := "https://vzigujbcjjmpntxhmyvr.supabase.co/functions/v1/llm/v1/chat/completions"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
