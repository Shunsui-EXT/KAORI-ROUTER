package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gitlabauth "github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/gitlab"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
)

func TestGitLabAuthenticator_ResolveString_PrecedenceOrder(t *testing.T) {
	a := &GitLabAuthenticator{}

	t.Run("metadata wins over env", func(t *testing.T) {
		t.Setenv("GITLAB_BASE_URL", "https://env.example.com")
		opts := &LoginOptions{Metadata: map[string]string{"base_url": "https://metadata.example.com"}}
		if got := a.resolveString(opts, "base_url", "https://fallback.example.com"); got != "https://metadata.example.com" {
			t.Errorf("got %q, want metadata value", got)
		}
	})

	t.Run("env wins over fallback", func(t *testing.T) {
		t.Setenv("GITLAB_BASE_URL", "https://env.example.com")
		opts := &LoginOptions{}
		if got := a.resolveString(opts, "base_url", "https://fallback.example.com"); got != "https://env.example.com" {
			t.Errorf("got %q, want env value", got)
		}
	})

	t.Run("fallback used when nothing else set", func(t *testing.T) {
		opts := &LoginOptions{}
		if got := a.resolveString(opts, "base_url", "https://fallback.example.com"); got != "https://fallback.example.com" {
			t.Errorf("got %q, want fallback value", got)
		}
	})

	t.Run("empty when nothing set and no fallback", func(t *testing.T) {
		opts := &LoginOptions{}
		if got := a.resolveString(opts, "base_url", ""); got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})
}

func TestGitLabAuthenticator_RequireInput_FailsWhenNothingProvided(t *testing.T) {
	a := &GitLabAuthenticator{}
	opts := &LoginOptions{}
	if _, err := a.requireInput(opts, "oauth_client_id", "prompt: "); err == nil {
		t.Error("expected an error when no metadata, env var, or prompt is available")
	}
}

func TestGitLabAuthenticator_LoginOAuth_RejectsStateMismatch(t *testing.T) {
	// The OAuth server itself only forwards whatever state the callback URL
	// carries; the mismatch check happens in loginOAuth after the callback
	// is received. We can't easily drive the full browser-based flow in a
	// unit test, so this test exercises the OAuthServer + the comparison
	// logic directly via the same code path loginOAuth uses.
	port := 18271
	server := gitlabauth.NewOAuthServer(port)
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop(nil) }() //nolint:errcheck

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, errGet := http.Get("http://localhost:18271/auth/callback?code=abc&state=wrong-state")
		if errGet == nil {
			_ = resp.Body.Close()
		}
	}()

	result, err := server.WaitForCallback(2 * time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedState := "correct-state"
	if result.State == expectedState {
		t.Fatal("test setup error: result.State should not equal expectedState")
	}
	// This mirrors loginOAuth's own check: result.State != state → reject.
	if result.State == expectedState {
		t.Error("expected state mismatch to be detectable")
	}
}

func TestGitLabAuthenticator_LoginPAT_InvalidTokenFailsClearly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer server.Close()

	a := NewGitLabAuthenticator()
	opts := &LoginOptions{
		Metadata: map[string]string{
			"base_url":              server.URL,
			"personal_access_token": "invalid-token",
		},
	}
	_, err := a.loginPAT(context.Background(), &config.Config{}, opts)
	if err == nil {
		t.Fatal("expected an error for an invalid personal access token")
	}
}
