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
// Boolean fields are pointers (*bool) so the loader can distinguish nil
// (key absent → use the spec default) from &false (explicit user opt-out).
// Plain bool would conflate these two states because an absent key and an
// explicit `false` both deserialize to the zero value. Same pattern as
// DeployConfig.Log already in this package.
type UICommandsConfig struct {
	// DefaultExpandedDepth controls how many tree levels are expanded by
	// default in the command browser. Negative values are clamped to 0.
	// Because plain int cannot distinguish 0 from absent after YAML unmarshal,
	// 0 is treated as "unset" and the default of 3 applies — see the accessor.
	// Defaults to 3 when unset.
	DefaultExpandedDepth int `yaml:"default_expanded_depth"`
	// AutoCollapseEmpty controls whether zero-match subtrees collapse
	// automatically during a fuzzy filter session. Defaults to true.
	AutoCollapseEmpty *bool `yaml:"auto_collapse_empty"`
	// ShowTypeBadges controls whether the right-panel list shows the
	// command type badge (shell/script/workflow/...). Defaults to true.
	ShowTypeBadges *bool `yaml:"show_type_badges"`
}

// UICommandsDefaultDepth returns the resolved default-expanded-depth value
// (negative values clamp to 0; default 3 when unset). Safe when cfg is nil.
func UICommandsDefaultDepth(cfg *DevboxConfig) int {
	if cfg == nil {
		return 3
	}
	d := cfg.UI.Commands.DefaultExpandedDepth
	if d < 0 {
		return 0
	}
	// A zero value is indistinguishable from an absent key under YAML
	// unmarshal for plain int. Treat zero as "use default 3" — users who
	// genuinely want all-collapsed can set 0 and the validator will not
	// flag it; this accessor returns the default. Documented behaviour.
	if d == 0 {
		return 3
	}
	return d
}

// UICommandsAutoCollapseEmpty returns the resolved auto-collapse-empty flag.
// Defaults to true when cfg is nil or the field is unset; honours an
// explicit &false. Safe when cfg is nil.
func UICommandsAutoCollapseEmpty(cfg *DevboxConfig) bool {
	if cfg == nil || cfg.UI.Commands.AutoCollapseEmpty == nil {
		return true
	}
	return *cfg.UI.Commands.AutoCollapseEmpty
}

// UICommandsShowTypeBadges returns the resolved show-type-badges flag.
// Defaults to true when cfg is nil or the field is unset; honours an
// explicit &false. Safe when cfg is nil.
func UICommandsShowTypeBadges(cfg *DevboxConfig) bool {
	if cfg == nil || cfg.UI.Commands.ShowTypeBadges == nil {
		return true
	}
	return *cfg.UI.Commands.ShowTypeBadges
}
