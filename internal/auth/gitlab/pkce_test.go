package gitlab

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGeneratePKCECodes_ProducesValidS256Challenge(t *testing.T) {
	codes, err := GeneratePKCECodes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if codes.CodeVerifier == "" || codes.CodeChallenge == "" {
		t.Fatalf("expected non-empty verifier and challenge, got %+v", codes)
	}
	sum := sha256.Sum256([]byte(codes.CodeVerifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if codes.CodeChallenge != want {
		t.Errorf("challenge is not the S256 hash of the verifier: got %q, want %q", codes.CodeChallenge, want)
	}
}

func TestGeneratePKCECodes_ProducesUniqueValues(t *testing.T) {
	a, err := GeneratePKCECodes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := GeneratePKCECodes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.CodeVerifier == b.CodeVerifier {
		t.Error("expected two calls to produce different verifiers")
	}
}
