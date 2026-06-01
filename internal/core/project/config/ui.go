package config

// UIConfig holds UI-related configuration loaded from the optional
// `ui:` block in devbox.yml. The block uses the same lenient loader
// as the rest of devbox.yml — an absent block and unknown keys are
// silently ignored at load time; the dedicated `ui` validator surfaces
// explicit feedback (unknown keys → warning, invalid values → error).
type UIConfig struct {
	Commands UICommandsConfig `yaml:"commands"`
}

// UICommandsConfig configures the interactive command browser used by
// `devbox commands run` / `inspect` when invoked without an exact ID.
//
// All fields are pointers so the loader can distinguish nil (key absent →
// use the spec default) from an explicit zero/false value. Plain int/bool
// would conflate these two states because an absent key and an explicit 0/false
// both deserialize to the zero value. Same pattern as DeployConfig.Log *bool
// already in this package.
type UICommandsConfig struct {
	// DefaultExpandedDepth controls how many tree levels are expanded by
	// default in the command browser. Negative values are clamped to 0;
	// 0 means all-collapsed. Defaults to 1 when nil (key absent).
	DefaultExpandedDepth *int `yaml:"default_expanded_depth"`
	// AutoCollapseEmpty controls whether zero-match subtrees collapse
	// automatically during a fuzzy filter session. Defaults to true.
	AutoCollapseEmpty *bool `yaml:"auto_collapse_empty"`
	// ShowTypeBadges controls whether the right-panel list shows the
	// command type badge (shell/script/workflow/...). Defaults to true.
	ShowTypeBadges *bool `yaml:"show_type_badges"`
}

// UICommandsDefaultDepth returns the resolved default-expanded-depth value
// (nil → 1; negative values clamp to 0; explicit 0 = all-collapsed). Safe when cfg is nil.
func UICommandsDefaultDepth(cfg *DweConfig) int {
	if cfg == nil || cfg.UI.Commands.DefaultExpandedDepth == nil {
		return 1
	}
	d := *cfg.UI.Commands.DefaultExpandedDepth
	if d < 0 {
		return 0
	}
	return d
}

// UICommandsAutoCollapseEmpty returns the resolved auto-collapse-empty flag.
// Defaults to true when cfg is nil or the field is unset; honours an
// explicit &false. Safe when cfg is nil.
func UICommandsAutoCollapseEmpty(cfg *DweConfig) bool {
	if cfg == nil || cfg.UI.Commands.AutoCollapseEmpty == nil {
		return true
	}
	return *cfg.UI.Commands.AutoCollapseEmpty
}

// UICommandsShowTypeBadges returns the resolved show-type-badges flag.
// Defaults to true when cfg is nil or the field is unset; honours an
// explicit &false. Safe when cfg is nil.
func UICommandsShowTypeBadges(cfg *DweConfig) bool {
	if cfg == nil || cfg.UI.Commands.ShowTypeBadges == nil {
		return true
	}
	return *cfg.UI.Commands.ShowTypeBadges
}
