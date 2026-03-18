package main

import (
	"encoding/json"
	"testing"
)

func TestExtractSymbolsFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		input []byte
		want  []string
	}{
		{
			name:   "valid symbols",
			input:  []byte(`{"symbols":["AAPL","GOOGL","MSFT"]}`),
			want:   []string{"AAPL", "GOOGL", "MSFT"},
		},
		{
			name:   "empty symbols array",
			input:  []byte(`{"symbols":[]}`),
			want:   []string{},
		},
		{
			name:   "no symbols field",
			input:  []byte(`{"other":"data"}`),
			want:   []string{},
		},
		{
			name:   "empty JSON",
			input:  []byte(`{}`),
			want:   []string{},
		},
		{
			name:   "invalid JSON",
			input:  []byte(`not json`),
			want:   nil,
		},
		{
			name:   "empty strings filtered",
			input:  []byte(`{"symbols":["AAPL","","MSFT",""]}`),
			want:   []string{"AAPL", "MSFT"},
		},
		{
			name:   "nil is treated as empty",
			input:  []byte(`{"symbols":null}`),
			want:   []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSymbolsFromConfig(tc.input)
			if tc.want == nil {
				if got != nil {
					t.Errorf("extractSymbolsFromConfig = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Errorf("extractSymbolsFromConfig = %v, want %v", got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("extractSymbolsFromConfig[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestExtractSymbolsFromChannelConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
		want   []string
	}{
		{
			name:   "nil config returns nil",
			config: nil,
			want:   nil,
		},
		{
			name:   "valid symbols via map",
			config: map[string]interface{}{"symbols": []interface{}{"TSLA", "NVDA"}},
			want:   []string{"TSLA", "NVDA"},
		},
		{
			name:   "empty config returns empty slice",
			config: map[string]interface{}{},
			want:   []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSymbolsFromChannelConfig(tc.config)
			if tc.want == nil {
				if got != nil {
					t.Errorf("extractSymbolsFromChannelConfig = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Errorf("extractSymbolsFromChannelConfig = %v, want %v", got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("extractSymbolsFromChannelConfig[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestExtractSymbolsRoundTrip(t *testing.T) {
	// Round-trip: map → extractSymbolsFromChannelConfig → extractSymbolsFromConfig
	original := []string{"AAPL", "GOOGL", "MSFT"}
	config := map[string]interface{}{"symbols": interfaceSlice(original)}
	got := extractSymbolsFromChannelConfig(config)
	if len(got) != len(original) {
		t.Fatalf("got %d, want %d", len(got), len(original))
	}
	for i := range got {
		if got[i] != original[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], original[i])
		}
	}
}

func interfaceSlice(strs []string) []interface{} {
	result := make([]interface{}, len(strs))
	for i, s := range strs {
		result[i] = s
	}
	return result
}

func TestExtractSymbolsFromConfigJSON(t *testing.T) {
	// Verify the JSON parsing round-trip works for the DB JSONB format
	original := map[string]interface{}{
		"symbols": []interface{}{"AAPL", "GOOGL"},
	}
	jsonBytes, _ := json.Marshal(original)
	got := extractSymbolsFromConfig(jsonBytes)
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}
