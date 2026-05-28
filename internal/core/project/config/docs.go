package config

// DocsConfig holds documentation-related configuration loaded from the optional
// `docs:` block in devbox.yml. The block uses the same lenient loader
// as the rest of devbox.yml — an absent block and unknown keys are
// silently ignored at load time; the dedicated `docs` validator surfaces
// explicit feedback (unknown keys → warning, invalid values → error).
type DocsConfig struct {
	Mermaid     string `yaml:"mermaid"`
	CacheSizeMB int    `yaml:"cache_size_mb"`
}

// MermaidMode returns the resolved mermaid rendering mode (default: "auto").
// Valid values: "auto" (try mmdc if available), "mmdc" (require mmdc),
// "off" (disable). Safe when cfg is nil.
func MermaidMode(cfg *DevboxConfig) string {
	if cfg == nil || cfg.Docs.Mermaid == "" {
		return "auto"
	}
	return cfg.Docs.Mermaid
}

// MermaidCacheSizeMB returns the resolved cache size limit in MB (default: 100).
// Zero or negative values are clamped to 100. Safe when cfg is nil.
func MermaidCacheSizeMB(cfg *DevboxConfig) int {
	if cfg == nil {
		return 100
	}
	size := cfg.Docs.CacheSizeMB
	if size <= 0 {
		return 100
	}
	return size
}
