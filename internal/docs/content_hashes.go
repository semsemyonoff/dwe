package docs

// ContentHashFor returns the content hash for a given relative path,
// or an empty string if the manifest does not contain an entry for this path.
// An empty string means the staleness check is disabled for this file.
func ContentHashFor(relPath string) string {
	if h, ok := ContentHashes[relPath]; ok {
		return h
	}
	return ""
}
