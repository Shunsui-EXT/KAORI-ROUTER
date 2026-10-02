package alysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInitiateDeviceFlow_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DeviceCodePath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("apikey") != AnonKey {
			t.Errorf("expected apikey header to be the anon key")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:              "dc-123",
			UserCode:                "ABCD-1234",
			VerificationURLComplete: "https://alysiscode.com/activate?code=ABCD-1234",
			ExpiresIn:               900,
			Interval:                5,
		})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	grant, err := auth.InitiateDeviceFlow(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant.DeviceCode != "dc-123" || grant.UserCode != "ABCD-1234" {
		t.Errorf("unexpected grant: %+v", grant)
	}
}

func TestPollForToken_ApprovedOnFirstProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DeviceTokenPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "approved", Key: "slk_test123"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	status, err := auth.PollForToken(context.Background(), "dc-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Key != "slk_test123" {
		t.Errorf("expected key slk_test123, got %q", status.Key)
	}
}

func TestPollForToken_Denied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "denied"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	_, err := auth.PollForToken(context.Background(), "dc-123")
	if err == nil {
		t.Fatal("expected an error when login is denied")
	}
}

func TestPollForToken_Expired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "expired"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	_, err := auth.PollForToken(context.Background(), "dc-123")
	if err == nil {
		t.Fatal("expected an error when the device code has expired")
	}
}

func TestPollForToken_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "not_found"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	_, err := auth.PollForToken(context.Background(), "dc-123")
	if err == nil {
		t.Fatal("expected an error when the device code is not found")
	}
}

func TestPollForToken_AlreadyClaimed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "already_claimed"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	auth := NewAuth()
	_, err := auth.PollForToken(context.Background(), "dc-123")
	if err == nil {
		t.Fatal("expected an error when the device code was already claimed")
	}
}

func TestPollForToken_RespectsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Status: "pending"})
	}))
	defer server.Close()

	originalURL := SupabaseURL
	SupabaseURL = server.URL
	defer func() { SupabaseURL = originalURL }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	auth := NewAuth()
	_, err := auth.PollForToken(ctx, "dc-123")
	if err == nil {
		t.Fatal("expected PollForToken to respect an already-cancelled context")
	}
}
