package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	a := &GitLabAuthenticator{CallbackPort: 18273}
	opts := &LoginOptions{
		NoBrowser:    true,
		CallbackPort: 18273,
		Metadata: map[string]string{
			"base_url":        "https://gitlab.example.com",
			"oauth_client_id": "test-client-id",
		},
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := a.loginOAuth(context.Background(), &config.Config{}, opts)
		errCh <- err
	}()

	// Give loginOAuth time to start its OAuthServer before we hit the callback.
	time.Sleep(200 * time.Millisecond)
	resp, errGet := http.Get("http://localhost:18273/auth/callback?code=abc123&state=wrong-state")
	if errGet == nil {
		_ = resp.Body.Close()
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error for a mismatched OAuth state")
		}
		if !strings.Contains(err.Error(), "state mismatch") {
			t.Errorf("expected a state-mismatch error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for loginOAuth to return")
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
