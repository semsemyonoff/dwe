package docs

import (
	"errors"
	"testing"
	"testing/fstest"
)

func TestParseTopic(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedPath   string
		expectedAnchor string
		expectError    bool
		errorMsg       string
	}{
		{
			name:         "simple topic",
			input:        "config/services",
			expectedPath: "config/services",
		},
		{
			name:           "topic with anchor",
			input:          "config/services#anchor",
			expectedPath:   "config/services",
			expectedAnchor: "anchor",
		},
		{
			name:           "topic with .md and anchor",
			input:          "config/services.md#anchor",
			expectedPath:   "config/services",
			expectedAnchor: "anchor",
		},
		{
			name:         "topic with .md",
			input:        "config/services.md",
			expectedPath: "config/services",
		},
		{
			name:        "empty input",
			input:       "",
			expectError: true,
			errorMsg:    "topic path cannot be empty",
		},
		{
			name:        "only anchor",
			input:       "#anchor",
			expectError: true,
			errorMsg:    "topic path cannot be empty",
		},
		{
			name:        "trailing slash",
			input:       "config/services/",
			expectError: true,
		},
		{
			name:           "spaces around components",
			input:          "config/services # anchor ",
			expectedPath:   "config/services",
			expectedAnchor: "anchor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, anchor, err := ParseTopic(tt.input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				if tt.errorMsg != "" && err.Error() != tt.errorMsg {
					t.Errorf("expected error %q, got %q", tt.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if path != tt.expectedPath {
				t.Errorf("expected path %q, got %q", tt.expectedPath, path)
			}

			if anchor != tt.expectedAnchor {
				t.Errorf("expected anchor %q, got %q", tt.expectedAnchor, anchor)
			}
		})
	}
}

func TestResolveExact(t *testing.T) {
	// Create a test tree
	testFS := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services")},
		"config/devbox.md":   &fstest.MapFile{Data: []byte("# Devbox")},
		"cli/reference.md":   &fstest.MapFile{Data: []byte("# CLI Reference")},
	}

	roots := []DocRoot{
		{
			Name: "devbox",
			FS:   testFS,
		},
	}

	result, err := Resolve(roots, "config/services", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Path != "config/services" {
		t.Errorf("expected path %q, got %q", "config/services", result.Path)
	}

	if result.Source != "devbox" {
		t.Errorf("expected source %q, got %q", "devbox", result.Source)
	}
}

func TestResolveFuzzyMatch(t *testing.T) {
	testFS := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services")},
		"cli/reference.md":   &fstest.MapFile{Data: []byte("# CLI Reference")},
	}

	roots := []DocRoot{
		{
			Name: "devbox",
			FS:   testFS,
		},
	}

	// Fuzzy match should find "config/services" when searching for "services"
	result, err := Resolve(roots, "services", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Path != "config/services" {
		t.Errorf("expected path %q, got %q", "config/services", result.Path)
	}
}

func TestResolveMultipleMatches(t *testing.T) {
	testFS := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services")},
		"services/deploy.md": &fstest.MapFile{Data: []byte("# Deploy")},
		"cli/reference.md":   &fstest.MapFile{Data: []byte("# CLI Reference")},
	}

	roots := []DocRoot{
		{
			Name: "devbox",
			FS:   testFS,
		},
	}

	_, err := Resolve(roots, "services", "en")

	var multiErr *MultipleMatchesError
	if !errors.As(err, &multiErr) {
		t.Fatalf("expected MultipleMatchesError, got %v", err)
	}

	if len(multiErr.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(multiErr.Candidates))
	}

	// Candidates should be sorted
	if multiErr.Candidates[0] != "config/services" || multiErr.Candidates[1] != "services/deploy" {
		t.Errorf("candidates not in expected order: %v", multiErr.Candidates)
	}
}

func TestResolveNotFound(t *testing.T) {
	testFS := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services")},
	}

	roots := []DocRoot{
		{
			Name: "devbox",
			FS:   testFS,
		},
	}

	_, err := Resolve(roots, "nonexistent", "en")

	var notFoundErr *NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}

	if notFoundErr.Topic != "nonexistent" {
		t.Errorf("expected topic %q, got %q", "nonexistent", notFoundErr.Topic)
	}
}

func TestAllTopics(t *testing.T) {
	testFS := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services")},
		"config/devbox.md":   &fstest.MapFile{Data: []byte("# Devbox")},
		"cli/reference.md":   &fstest.MapFile{Data: []byte("# CLI Reference")},
	}

	roots := []DocRoot{
		{
			Name: "devbox",
			FS:   testFS,
		},
	}

	topics := AllTopics(roots, "en")

	if len(topics) != 3 {
		t.Errorf("expected 3 topics, got %d", len(topics))
	}

	// Check that topics are sorted deterministically
	expected := []string{"cli/reference", "config/devbox", "config/services"}
	for i, topic := range topics {
		if i >= len(expected) {
			break
		}
		if topic.Path != expected[i] {
			t.Errorf("expected topic %q at index %d, got %q", expected[i], i, topic.Path)
		}
	}
}

func TestAllTopicsDeterministic(t *testing.T) {
	// Test that AllTopics returns the same order regardless of iteration order
	testFS := fstest.MapFS{
		"z-file.md": &fstest.MapFile{Data: []byte("# Z")},
		"a-file.md": &fstest.MapFile{Data: []byte("# A")},
		"m-file.md": &fstest.MapFile{Data: []byte("# M")},
	}

	roots := []DocRoot{
		{
			Name: "devbox",
			FS:   testFS,
		},
	}

	topics1 := AllTopics(roots, "en")
	topics2 := AllTopics(roots, "en")

	if len(topics1) != len(topics2) {
		t.Fatalf("inconsistent topic counts: %d vs %d", len(topics1), len(topics2))
	}

	for i := range topics1 {
		if topics1[i].Path != topics2[i].Path {
			t.Errorf("inconsistent topic at index %d: %q vs %q", i, topics1[i].Path, topics2[i].Path)
		}
	}
}
