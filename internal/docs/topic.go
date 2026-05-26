package docs

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ResolvedTopic represents a successfully resolved documentation topic.
type ResolvedTopic struct {
	Path      string // Relative path (e.g., "config/services")
	Anchor    string // Optional anchor (e.g., "anchor" from "config/services#anchor")
	Source    string // Source root name ("devbox" or "project")
	DisplayName string // Display name for the topic
}

// TopicEntry represents a single topic in the flat list.
type TopicEntry struct {
	Path        string // Relative path without .md
	DisplayName string // Display name
	Lang        string // Language ("en" or other locale code)
	Source      string // Root name ("devbox" or "project")
}

// ParseTopic splits a user input like "config/services#anchor" into path and anchor components.
// It trims .md extension if present and rejects empty paths.
func ParseTopic(input string) (path, anchor string, err error) {
	if input == "" {
		return "", "", errors.New("topic path cannot be empty")
	}

	// Split on # to separate path and anchor
	parts := strings.SplitN(input, "#", 2)
	path = strings.TrimSpace(parts[0])

	if len(parts) > 1 {
		anchor = strings.TrimSpace(parts[1])
	}

	// Trim trailing .md extension if present
	path = strings.TrimSuffix(path, ".md")

	// After trimming, ensure path is not empty
	if path == "" {
		return "", "", errors.New("topic path cannot be empty")
	}

	// Reject trailing slashes
	if strings.HasSuffix(path, "/") {
		return "", "", fmt.Errorf("invalid topic path: %q (no trailing slash)", path)
	}

	return path, anchor, nil
}

// NotFoundError is returned when a topic cannot be resolved.
type NotFoundError struct {
	Topic       string
	Suggestions []string
}

func (e *NotFoundError) Error() string {
	msg := fmt.Sprintf("topic %q not found", e.Topic)
	if len(e.Suggestions) > 0 {
		msg += ". Did you mean: " + strings.Join(e.Suggestions, ", ")
	}
	return msg
}

// MultipleMatchesError is returned when topic resolution is ambiguous.
type MultipleMatchesError struct {
	Topic      string
	Candidates []string
}

func (e *MultipleMatchesError) Error() string {
	return fmt.Sprintf("ambiguous topic %q; candidates: %s", e.Topic, strings.Join(e.Candidates, ", "))
}

// Resolve finds a topic across the given roots.
// First tries exact match, then falls back to case-insensitive substring matching.
// Returns NotFoundError or MultipleMatchesError on failure.
func Resolve(roots []DocRoot, topic string, locale string) (*ResolvedTopic, error) {
	// Build all topics to search across
	allTopics := AllTopics(roots, locale)

	// Try exact match first (case-sensitive)
	for _, entry := range allTopics {
		if entry.Path == topic {
			return &ResolvedTopic{
				Path:        entry.Path,
				Anchor:      "",
				Source:      entry.Source,
				DisplayName: entry.DisplayName,
			}, nil
		}
	}

	// Fall back to fuzzy matching (case-insensitive substring)
	fuzzyMatches := []TopicEntry{}
	topicLower := strings.ToLower(topic)

	for _, entry := range allTopics {
		if strings.Contains(strings.ToLower(entry.Path), topicLower) {
			fuzzyMatches = append(fuzzyMatches, entry)
		}
	}

	if len(fuzzyMatches) == 0 {
		// No matches at all
		suggestions := []string{}
		if len(allTopics) > 0 && len(allTopics) <= 10 {
			// Suggest the first few topics if not too many
			for i := 0; i < 3 && i < len(allTopics); i++ {
				suggestions = append(suggestions, allTopics[i].Path)
			}
		}
		return nil, &NotFoundError{
			Topic:       topic,
			Suggestions: suggestions,
		}
	}

	if len(fuzzyMatches) == 1 {
		// Exactly one match
		return &ResolvedTopic{
			Path:        fuzzyMatches[0].Path,
			Anchor:      "",
			Source:      fuzzyMatches[0].Source,
			DisplayName: fuzzyMatches[0].DisplayName,
		}, nil
	}

	// Multiple matches
	candidates := []string{}
	for _, entry := range fuzzyMatches {
		candidates = append(candidates, entry.Path)
	}
	sort.Strings(candidates)

	return nil, &MultipleMatchesError{
		Topic:      topic,
		Candidates: candidates,
	}
}

// AllTopics returns a flat list of all available topics across roots.
// Entries are sorted deterministically (by source, then by path).
func AllTopics(roots []DocRoot, locale string) []TopicEntry {
	topics := []TopicEntry{}
	seen := make(map[string]bool) // Track (source, path) pairs to avoid duplicates

	for _, root := range roots {
		tree, err := BuildTree(root)
		if err != nil {
			// Skip roots with errors
			continue
		}

		// Collect all markdown files from the tree
		collectTopics(tree, root.Name, &topics, seen)
	}

	// Sort for deterministic output: by source, then by path
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Source != topics[j].Source {
			// Put "devbox" before "project"
			if topics[i].Source == "devbox" {
				return true
			}
			if topics[j].Source == "devbox" {
				return false
			}
		}
		return topics[i].Path < topics[j].Path
	})

	return topics
}

func collectTopics(node *Node, source string, topics *[]TopicEntry, seen map[string]bool) {
	if !node.IsDir {
		// This is a file node
		// node.Path already contains the full relative path (set by BuildTree)
		// Remove .md extension for the topic path
		topicPath := strings.TrimSuffix(node.Path, ".md")

		// Skip empty paths (shouldn't happen, but be safe)
		if topicPath == "" {
			return
		}

		key := source + "|" + topicPath
		if !seen[key] {
			seen[key] = true
			*topics = append(*topics, TopicEntry{
				Path:        topicPath,
				DisplayName: node.Name,
				Lang:        "en", // TODO: Detect language from path
				Source:      source,
			})
		}
		return
	}

	// This is a directory node; recurse into children
	if node.Children != nil {
		for _, child := range node.Children {
			collectTopics(child, source, topics, seen)
		}
	}
}
