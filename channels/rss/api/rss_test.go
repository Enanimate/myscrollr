package main

import (
	"encoding/json"
	"testing"
)

func TestExtractFeedURLsFromConfig(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  []string
	}{
		{
			name:  "valid feeds",
			input: []byte(`{"feeds":[{"url":"https://example.com/feed1"},{"url":"https://example.com/feed2"}]}`),
			want:  []string{"https://example.com/feed1", "https://example.com/feed2"},
		},
		{
			name:  "single feed",
			input: []byte(`{"feeds":[{"url":"https://example.com/rss"}]}`),
			want:  []string{"https://example.com/rss"},
		},
		{
			name:  "empty feeds array",
			input: []byte(`{"feeds":[]}`),
			want:  []string{},
		},
		{
			name:  "no feeds field",
			input: []byte(`{"other":"data"}`),
			want:  []string{},
		},
		{
			name:  "invalid JSON",
			input: []byte(`not json`),
			want:  nil,
		},
		{
			name:  "feeds with extra fields",
			input: []byte(`{"feeds":[{"url":"https://a.com","name":"Feed A","category":"Tech"}]}`),
			want:  []string{"https://a.com"},
		},
		{
			name:  "empty url filtered",
			input: []byte(`{"feeds":[{"url":"https://a.com"},{"url":""},{"url":"https://b.com"}]}`),
			want:  []string{"https://a.com", "https://b.com"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFeedURLsFromConfig(tc.input)
			if tc.want == nil {
				if got != nil {
					t.Errorf("extractFeedURLsFromConfig = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Errorf("extractFeedURLsFromConfig = %v, want %v", got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("extractFeedURLsFromConfig[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestExtractFeedURLsFromChannelConfig(t *testing.T) {
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
			name:   "valid feeds via map",
			config: map[string]interface{}{
				"feeds": []interface{}{
					map[string]interface{}{"url": "https://blog.example.com/rss", "name": "Example Blog"},
				},
			},
			want: []string{"https://blog.example.com/rss"},
		},
		{
			name:   "empty config returns nil",
			config: map[string]interface{}{},
			want:   nil,
		},
		{
			name:   "feeds is not an array",
			config: map[string]interface{}{"feeds": "https://example.com/rss"},
			want:   nil,
		},
		{
			name: "multiple feeds via direct map construction",
			config: map[string]interface{}{
				"feeds": []interface{}{
					map[string]interface{}{"url": "https://news.example.com/feed", "name": "News"},
					map[string]interface{}{"url": "https://tech.example.com/rss", "name": "Tech"},
				},
			},
			want: []string{"https://news.example.com/feed", "https://tech.example.com/rss"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFeedURLsFromChannelConfig(tc.config)
			if tc.want == nil {
				if got != nil {
					t.Errorf("extractFeedURLsFromChannelConfig = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Errorf("extractFeedURLsFromChannelConfig = %v, want %v", got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("extractFeedURLsFromChannelConfig[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestExtractFeedURLsRoundTrip(t *testing.T) {
	original := []string{"https://blog.example.com/feed", "https://news.example.com/rss"}
	feeds := make([]interface{}, len(original))
	for i, url := range original {
		feeds[i] = map[string]interface{}{"url": url, "name": "Feed"}
	}
	config := map[string]interface{}{"feeds": feeds}
	got := extractFeedURLsFromChannelConfig(config)
	if len(got) != len(original) {
		t.Fatalf("got %d, want %d", len(got), len(original))
	}
	for i := range got {
		if got[i] != original[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], original[i])
		}
	}
}

func TestExtractFeedURLsJSONToChannelConfig(t *testing.T) {
	// Simulates what comes from the DB JSONB column
	original := map[string]interface{}{
		"feeds": []interface{}{
			map[string]interface{}{"url": "https://test.com/rss", "name": "Test"},
		},
	}
	jsonBytes, _ := json.Marshal(original)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	got := extractFeedURLsFromChannelConfig(parsed)
	if len(got) != 1 {
		t.Errorf("got %d, want 1", len(got))
	}
}
