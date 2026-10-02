package executor

import (
	"testing"

	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
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

func TestAlysisChatCompletionsURL(t *testing.T) {
	e := NewAlysisExecutor(nil)
	got := e.chatCompletionsURL()
	want := "https://vzigujbcjjmpntxhmyvr.supabase.co/functions/v1/llm/v1/chat/completions"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
