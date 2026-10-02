package gitlab

import (
	"net/http"
	"testing"
	"time"
)

func TestOAuthServer_HandleCallback_Success(t *testing.T) {
	port := 18171
	server := NewOAuthServer(port)
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop(nil) }() //nolint:errcheck

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, errGet := http.Get("http://localhost:18171/auth/callback?code=abc123&state=xyz789")
		if errGet == nil {
			_ = resp.Body.Close()
		}
	}()

	result, err := server.WaitForCallback(2 * time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Code != "abc123" || result.State != "xyz789" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestOAuthServer_HandleCallback_UserDeniedConsent(t *testing.T) {
	port := 18172
	server := NewOAuthServer(port)
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop(nil) }() //nolint:errcheck

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, errGet := http.Get("http://localhost:18172/auth/callback?error=access_denied")
		if errGet == nil {
			_ = resp.Body.Close()
		}
	}()

	result, err := server.WaitForCallback(2 * time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "access_denied" {
		t.Errorf("expected Error %q, got %+v", "access_denied", result)
	}
}

func TestMergeDirectAccessMetadata_RefreshIntervalClampsToUpperBound(t *testing.T) {
	metadata := map[string]any{}
	direct := &DirectAccessResponse{
		BaseURL:   "https://gitlab.example.com/ai",
		Token:     "gw-token",
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(), // ttl/2 = 1800s, well above the 240s ceiling
	}

	MergeDirectAccessMetadata(metadata, direct)

	if got := metadata["refresh_interval_seconds"]; got != 240 {
		t.Errorf("expected refresh_interval_seconds to clamp to 240, got %v", got)
	}
}

func TestMergeDirectAccessMetadata_RefreshIntervalClampsToLowerBound(t *testing.T) {
	metadata := map[string]any{}
	direct := &DirectAccessResponse{
		BaseURL:   "https://gitlab.example.com/ai",
		Token:     "gw-token",
		ExpiresAt: time.Now().Add(10 * time.Second).Unix(), // ttl/2 = 5s, below the 60s floor
	}

	MergeDirectAccessMetadata(metadata, direct)

	if got := metadata["refresh_interval_seconds"]; got != 60 {
		t.Errorf("expected refresh_interval_seconds to clamp to 60, got %v", got)
	}
}
