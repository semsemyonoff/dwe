package docs

import "strings"

// IsFenceLine reports whether a trimmed markdown line opens or closes a fenced
// code block (``` or ~~~). It is used by all callers that need to track
// inFence state while scanning document content.
func IsFenceLine(trim string) bool {
	return strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~")
}

// RootByName returns the first DocRoot in roots whose Name equals name.
// The zero-value DocRoot (Name == "") is returned when no match is found.
func RootByName(roots []DocRoot, name string) DocRoot {
	for _, r := range roots {
		if r.Name == name {
			return r
		}
	}
	return DocRoot{}
}
