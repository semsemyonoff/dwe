package docs

import (
	"testing"
)

func TestContentHashFor(t *testing.T) {
	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()

	ContentHashes = map[string]string{
		"config/services.md": "abcd1234efgh",
		"other.md":           "zzzzzzz",
	}

	tests := []struct {
		path string
		want string
	}{
		{"config/services.md", "abcd1234efgh"},
		{"other.md", "zzzzzzz"},
		{"missing.md", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ContentHashFor(tt.path)
			if got != tt.want {
				t.Errorf("ContentHashFor(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestContentHashFor_EmptyManifest(t *testing.T) {
	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()

	ContentHashes = map[string]string{}

	got := ContentHashFor("any/path.md")
	if got != "" {
		t.Errorf("ContentHashFor with empty manifest should return empty string, got %q", got)
	}
}
