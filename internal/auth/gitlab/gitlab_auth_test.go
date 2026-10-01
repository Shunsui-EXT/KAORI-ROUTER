// internal/auth/gitlab/gitlab_auth_test.go
package gitlab

import (
	"net/http"
	"testing"
	"time"
)

func TestOAuthServer_HandleCallback_Success(t *testing.T) {
	server := NewOAuthServer(0) // port 0 would fail isPortAvailable's bind check in Start; use a free port instead
	port := 18171
	server = NewOAuthServer(port)
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
