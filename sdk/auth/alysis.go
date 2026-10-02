package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	coreauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
)

// AlysisAuthenticator implements the device-flow login for Alysis Code Pro.
type AlysisAuthenticator struct{}

// NewAlysisAuthenticator constructs an Alysis authenticator.
func NewAlysisAuthenticator() *AlysisAuthenticator {
	return &AlysisAuthenticator{}
}

// Provider returns the provider key for Alysis Code Pro.
func (a *AlysisAuthenticator) Provider() string {
	return "alysis"
}

// RefreshLead disables scheduled refresh: the gateway key is long-lived and
// has no refresh endpoint.
func (a *AlysisAuthenticator) RefreshLead() *time.Duration {
	return nil
}

// Login runs the Alysis Code device flow: request a user code, have the user
// approve it on https://alysiscode.com/activate, then persist the gateway key.
func (a *AlysisAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	authSvc := alysis.NewAuth()

	fmt.Println("Initiating Alysis device authentication...")
	grant, err := authSvc.InitiateDeviceFlow(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate device flow: %w", err)
	}

	verificationURL := grant.VerificationURLComplete
	if verificationURL == "" {
		verificationURL = grant.VerificationURL
	}
	if verificationURL == "" {
		verificationURL = alysis.ProductSiteURL + "/activate"
	}
	fmt.Printf("Please visit: %s\n", verificationURL)
	fmt.Printf("And approve code: %s\n", grant.UserCode)

	fmt.Println("Waiting for authorization...")
	status, err := authSvc.PollForToken(ctx, grant.DeviceCode, time.Duration(grant.Interval)*time.Second)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	fmt.Println("Alysis Code Pro authentication successful.")

	email := ""
	if opts.Metadata != nil {
		email = opts.Metadata["email"]
	}
	ts := &alysis.TokenStorage{
		Key:   status.Key,
		Type:  "alysis",
		Email: email,
	}

	fileName := alysis.CredentialFileName(ts.Email)
	metadata := map[string]any{
		"type":       "alysis",
		"gatewayKey": status.Key,
		"email":      ts.Email,
		"auth_kind":  "oauth",
	}

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Label:    "Alysis Code Pro",
		Storage:  ts,
		Metadata: metadata,
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	}, nil
}
