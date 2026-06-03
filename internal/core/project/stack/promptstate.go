package stack

// HealthState maps a Health enum value to the prompt-cache state string
// ("running" | "partial" | "stopped"). Unknown values return "stopped".
// Pure function, zero IO, zero deps beyond the Health type.
//
// The returned literals match the on-disk schema of .dwe/prompt-cache.yml
// (also exposed as promptcache.StateRunning/StatePartial/StateStopped). They are
// duplicated here rather than imported to keep core/ independent of shared/promptcache.
func HealthState(h Health) string {
	switch h {
	case HealthRunning:
		return "running"
	case HealthPartial:
		return "partial"
	case HealthStopped:
		return "stopped"
	default:
		return "stopped"
	}
}
