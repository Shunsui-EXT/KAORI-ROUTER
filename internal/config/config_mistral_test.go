package config

import "testing"

func TestMistralConfigDropsUnusableKeys(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(`mistral-api-key:
  - {}
  - api-key: "   "
  - api-key: " ms-valid "
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MistralKey) != 1 {
		t.Fatalf("got %d keys, want only the 1 entry with a non-empty api-key", len(cfg.MistralKey))
	}
	if cfg.MistralKey[0].APIKey != "ms-valid" {
		t.Fatalf("valid key not normalized: %#v", cfg.MistralKey[0])
	}
}

func TestMistralConfigDefaultsBaseURL(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(`mistral-api-key:
  - api-key: "ms-valid"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MistralKey) != 1 {
		t.Fatalf("got %d keys, want 1", len(cfg.MistralKey))
	}
	if cfg.MistralKey[0].BaseURL != "https://api.mistral.ai" {
		t.Fatalf("expected default base-url, got %q", cfg.MistralKey[0].BaseURL)
	}
}

func TestMistralConfigPreservesExplicitBaseURL(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(`mistral-api-key:
  - api-key: "ms-valid"
    base-url: "https://custom.mistral.example/v1"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MistralKey) != 1 {
		t.Fatalf("got %d keys, want 1", len(cfg.MistralKey))
	}
	if cfg.MistralKey[0].BaseURL != "https://custom.mistral.example/v1" {
		t.Fatalf("explicit base-url was not preserved, got %q", cfg.MistralKey[0].BaseURL)
	}
}
