package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
)

func TestAlysisAuthenticator_Provider(t *testing.T) {
	a := NewAlysisAuthenticator()
	if a.Provider() != "alysis" {
		t.Errorf("expected provider %q, got %q", "alysis", a.Provider())
	}
}

func TestAlysisAuthenticator_RefreshLead_IsNil(t *testing.T) {
	a := NewAlysisAuthenticator()
	if a.RefreshLead() != nil {
		t.Error("expected RefreshLead to be nil: the gateway key is long-lived with no refresh endpoint")
	}
}

func TestAlysisAuthenticator_Login_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case alysis.DeviceCodePath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceCodeResponse{
				DeviceCode: "dc-xyz", UserCode: "WXYZ-9876",
				VerificationURLComplete: "https://alysiscode.com/activate?code=WXYZ-9876",
				ExpiresIn:               900, Interval: 5,
			})
		case alysis.DeviceTokenPath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceTokenResponse{Status: "approved", Key: "slk_abc123"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	originalURL := alysis.SupabaseURL
	alysis.SupabaseURL = server.URL
	defer func() { alysis.SupabaseURL = originalURL }()

	a := NewAlysisAuthenticator()
	record, err := a.Login(context.Background(), &config.Config{}, &LoginOptions{Metadata: map[string]string{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Provider != "alysis" {
		t.Errorf("expected provider %q, got %q", "alysis", record.Provider)
	}
	gatewayKey, _ := record.Metadata["gatewayKey"].(string)
	if gatewayKey != "slk_abc123" {
		t.Errorf("expected gatewayKey %q in metadata, got %q", "slk_abc123", gatewayKey)
	}
	if record.Storage == nil {
		t.Error("expected Storage to be set so the generic manager can persist the credential")
	}
}

func TestAlysisAuthenticator_Login_Denied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case alysis.DeviceCodePath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceCodeResponse{
				DeviceCode: "dc-denied", UserCode: "DENY-0000", ExpiresIn: 900, Interval: 5,
			})
		case alysis.DeviceTokenPath:
			_ = json.NewEncoder(w).Encode(alysis.DeviceTokenResponse{Status: "denied"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	originalURL := alysis.SupabaseURL
	alysis.SupabaseURL = server.URL
	defer func() { alysis.SupabaseURL = originalURL }()

	a := NewAlysisAuthenticator()
	_, err := a.Login(context.Background(), &config.Config{}, &LoginOptions{Metadata: map[string]string{}})
	if err == nil {
		t.Fatal("expected an error when the device-flow login is denied")
	}
}
