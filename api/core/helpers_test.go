package core

import (
	"os"
	"testing"
)

func TestCleanFQDN(t *testing.T) {
	tests := []struct {
		name   string
		envVal string
		want   string
	}{
		{"https scheme stripped", "https://api.myscrollr.com", "api.myscrollr.com"},
		{"http scheme stripped", "http://api.myscrollr.com", "api.myscrollr.com"},
		{"no scheme", "api.myscrollr.com", "api.myscrollr.com"},
		{"trailing slash stripped", "api.myscrollr.com/", "api.myscrollr.com"},
		{"https with trailing slash", "https://api.myscrollr.com/", "api.myscrollr.com"},
		{"http with trailing slash", "http://api.myscrollr.com/", "api.myscrollr.com"},
		{"empty returns empty", "", ""},
		{"full URL with path (unexpected but strips scheme)", "https://api.myscrollr.com/v1", "api.myscrollr.com/v1"},
		{"double scheme prefix", "https://https://api.myscrollr.com", "https://api.myscrollr.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envVal == "" {
				os.Unsetenv("COOLIFY_FQDN")
			} else {
				os.Setenv("COOLIFY_FQDN", tc.envVal)
			}
			got := CleanFQDN()
			if got != tc.want {
				t.Errorf("CleanFQDN() with COOLIFY_FQDN=%q = %q, want %q", tc.envVal, got, tc.want)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		fallback string
		want     string
	}{
		{"empty uses fallback", "", "https://default.com", "https://default.com"},
		{"http preserved", "http://example.com", "https://fallback.com", "http://example.com"},
		{"https preserved", "https://example.com", "https://fallback.com", "https://example.com"},
		{"no scheme gets https prefix", "example.com", "https://fallback.com", "https://example.com"},
		{"trailing slash stripped", "https://example.com/", "https://fallback.com", "https://example.com"},
		{"whitespace trimmed", "  https://example.com  ", "https://fallback.com", "https://example.com"},
		{"empty fallback preserved", "", "", ""},
		{"no scheme no trailing slash", "example.com", "fallback.com", "https://example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateURL(tc.url, tc.fallback)
			if got != tc.want {
				t.Errorf("ValidateURL(%q, %q) = %q, want %q", tc.url, tc.fallback, got, tc.want)
			}
		})
	}
}
