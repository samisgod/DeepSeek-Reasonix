package config

import "testing"

func TestCommandCodeEndpointContract(t *testing.T) {
	cases := []struct {
		id        string
		kind      string
		request   string
		authHead  bool
		stateless bool
	}{
		{"commandcode-chat", "openai", "https://api.commandcode.ai/provider/v1/chat/completions", false, false},
		{"commandcode-anthropic", "anthropic", "https://api.commandcode.ai/provider/v1/messages", true, false},
		{"commandcode-responses", "responses", "https://api.commandcode.ai/provider/v1/responses", false, true},
	}
	for _, tc := range cases {
		preset, ok := CuratedProviderPreset(tc.id)
		if !ok || len(preset.Entries) != 1 {
			t.Fatalf("missing single-entry preset %q", tc.id)
		}
		entry := preset.Entries[0]
		if entry.Kind != tc.kind {
			t.Fatalf("%s kind = %q, want %q", tc.id, entry.Kind, tc.kind)
		}
		if entry.AuthHeader != tc.authHead {
			t.Fatalf("%s auth_header = %v, want %v", tc.id, entry.AuthHeader, tc.authHead)
		}
		if got := entry.ResponsesMode == "stateless"; got != tc.stateless {
			t.Fatalf("%s responses_mode = %q, want stateless=%v", tc.id, entry.ResponsesMode, tc.stateless)
		}
		if got := ProviderRequestURL(entry.Kind, entry.BaseURL); got != tc.request {
			t.Fatalf("%s request URL = %q, want %q", tc.id, got, tc.request)
		}
		if err := ValidateProviderEndpoint(&entry); err != nil {
			t.Fatalf("%s endpoint contract: %v", tc.id, err)
		}
	}
}
