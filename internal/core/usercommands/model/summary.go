package model

// CommandSummary is the shared agent-facing projection of a command. It carries
// DECLARED state: `hide:` is not evaluated by the builder — whether hidden
// commands are absent depends solely on whether the caller ran
// Registry.ApplyVisibility first. See usercommands.CommandIndex.
type CommandSummary struct {
	ID          string
	Group       string
	Description string
	Type        string
	// Service is EffectiveService(): the compose service / container name. It
	// may be an unrendered ${...} expression — consumers must not treat it as
	// a resolved name.
	Service string
}

// CommandGroupSummary is one AUTHORED group node with its declared command
// count. Synthetic ancestors materialized by the registry (a dotted group with
// no title, no description and no direct commands) are not represented. Like
// CommandSummary it reflects declared state, not a live listing: Count is not a
// promise about what `dwe commands list <ID>` prints while a `hide:` is active.
type CommandGroupSummary struct {
	ID          string
	Title       string
	Description string
	// Count is len(reg.List(ID)) in the registry state passed to the builder.
	Count int
}
