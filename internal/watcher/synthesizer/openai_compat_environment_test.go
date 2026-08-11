package synthesizer

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func Test_ConfigSynthesizer_Synthesize_resolves_openai_compat_api_key_from_environment_reference(t *testing.T) {
	// Given
	t.Setenv("AZURE_OPENAI_API_KEY", "resolved-key")
	synth := NewConfigSynthesizer()
	ctx := &SynthesisContext{
		Config: &config.Config{OpenAICompatibility: []config.OpenAICompatibility{{
			Name:    "azure-openai",
			BaseURL: "https://example.openai.azure.com/openai/v1",
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{
				APIKey: "${AZURE_OPENAI_API_KEY}",
			}},
		}}},
		Now:         time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		IDGenerator: NewStableIDGenerator(),
	}

	// When
	auths, err := synth.Synthesize(ctx)

	// Then
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("auth count = %d, want 1", len(auths))
	}
	if got := auths[0].Attributes["api_key"]; got != "resolved-key" {
		t.Fatalf("api_key = %q, want resolved-key", got)
	}
}

func Test_ConfigSynthesizer_Synthesize_omits_unresolved_openai_compat_environment_reference(t *testing.T) {
	// Given
	synth := NewConfigSynthesizer()
	ctx := &SynthesisContext{
		Config: &config.Config{OpenAICompatibility: []config.OpenAICompatibility{{
			Name:    "azure-openai",
			BaseURL: "https://example.openai.azure.com/openai/v1",
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{
				APIKey: "${MISSING_AZURE_OPENAI_API_KEY}",
			}},
		}}},
		Now:         time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		IDGenerator: NewStableIDGenerator(),
	}

	// When
	auths, err := synth.Synthesize(ctx)

	// Then
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("auth count = %d, want 1", len(auths))
	}
	if got := auths[0].Attributes["api_key"]; got != "" {
		t.Fatalf("api_key = %q, want empty", got)
	}
}
