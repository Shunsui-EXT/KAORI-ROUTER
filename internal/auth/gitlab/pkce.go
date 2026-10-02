package gitlab

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCECodes contains the PKCE code verifier and code challenge pair.
type PKCECodes struct {
	CodeVerifier  string
	CodeChallenge string
}

// GeneratePKCECodes generates a random code verifier and its S256 challenge.
func GeneratePKCECodes() (*PKCECodes, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("gitlab pkce generation failed: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return &PKCECodes{
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
	}, nil
}
