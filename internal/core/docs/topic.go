package docs

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ResolvedTopic represents a successfully resolved documentation topic.
type ResolvedTopic struct {
	Path        string // Relative path (e.g., "config/services")
	Anchor      string // Optional anchor (e.g., "anchor" from "config/services#anchor")
	Source      string // Source root name ("devbox" or "project")
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

// MaxAmbiguousCandidates caps how many candidate paths are returned in a
// MultipleMatchesError. The full count is preserved on Total so callers can
// render "and N more" without re-enumerating.
const MaxAmbiguousCandidates = 10

// MultipleMatchesError is returned when topic resolution is ambiguous.
// Candidates is capped at MaxAmbiguousCandidates; Total holds the full count.
type MultipleMatchesError struct {
	Topic      string
	Candidates []string
	Total      int
}

func (e *MultipleMatchesError) Error() string {
	return fmt.Sprintf("ambiguous topic %q; candidates: %s", e.Topic, strings.Join(e.Candidates, ", "))
}

// Resolve finds a topic across the given roots.
//
// Match ranking, in order — the first tier with exactly one hit resolves; a
// tier with two or more hits stops the search and yields a MultipleMatchesError
// scoped to that tier (so an ambiguous segment match never gets buried under a
// flood of substring matches):
//
//  1. exact path (case-sensitive)
//  2. last path segment equals topic (case-insensitive) — `services` prefers
//     `reference/config/services` over `reference/commands/services/...`
//  3. any path segment equals topic (case-insensitive)
//  4. case-insensitive substring
//
// Returns NotFoundError when no tier matches, or MultipleMatchesError with a
// capped candidate list when the narrowest non-empty tier has multiple hits.
func Resolve(roots []DocRoot, topic string, locale string) (*ResolvedTopic, error) {
	allTopics := AllTopics(roots, locale)

	// Tier 1: exact path (case-sensitive).
	for _, entry := range allTopics {
		if entry.Path == topic {
			return resolvedFrom(entry), nil
		}
	}

	// Tier 2: last-segment exact (case-insensitive).
	lastSegMatches := filterTopics(allTopics, func(p string) bool {
		return strings.EqualFold(lastSegment(p), topic)
	})
	if len(lastSegMatches) == 1 {
		return resolvedFrom(lastSegMatches[0]), nil
	}

	// Tier 3: any-segment exact (case-insensitive).
	segMatches := filterTopics(allTopics, func(p string) bool {
		for seg := range strings.SplitSeq(p, "/") {
			if strings.EqualFold(seg, topic) {
				return true
			}
		}
		return false
	})
	if len(segMatches) == 1 {
		return resolvedFrom(segMatches[0]), nil
	}

	// Tier 4: case-insensitive substring.
	topicLower := strings.ToLower(topic)
	fuzzyMatches := filterTopics(allTopics, func(p string) bool {
		return strings.Contains(strings.ToLower(p), topicLower)
	})

	if len(fuzzyMatches) == 0 {
		suggestions := []string{}
		if len(allTopics) > 0 && len(allTopics) <= 10 {
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
		return resolvedFrom(fuzzyMatches[0]), nil
	}

	// Ambiguous: pick the narrowest non-empty tier so the error doesn't dump
	// the full substring set when a tighter scoping was available.
	narrowest := fuzzyMatches
	switch {
	case len(lastSegMatches) >= 2:
		narrowest = lastSegMatches
	case len(segMatches) >= 2:
		narrowest = segMatches
	}

	candidates := make([]string, 0, len(narrowest))
	for _, entry := range narrowest {
		candidates = append(candidates, entry.Path)
	}
	sort.Strings(candidates)

	total := len(candidates)
	if total > MaxAmbiguousCandidates {
		candidates = candidates[:MaxAmbiguousCandidates]
	}

	return nil, &MultipleMatchesError{
		Topic:      topic,
		Candidates: candidates,
		Total:      total,
	}
}

func resolvedFrom(e TopicEntry) *ResolvedTopic {
	return &ResolvedTopic{
		Path:        e.Path,
		Anchor:      "",
		Source:      e.Source,
		DisplayName: e.DisplayName,
	}
}

func filterTopics(topics []TopicEntry, pred func(path string) bool) []TopicEntry {
	out := make([]TopicEntry, 0, len(topics))
	for _, e := range topics {
		if pred(e.Path) {
			out = append(out, e)
		}
	}
	return out
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
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
