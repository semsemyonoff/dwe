// Package scaffold contains the domain logic behind `dwe init`: it computes the
// file plan for a fresh DWE project, renders the embedded templates, and writes
// them to disk atomically.
//
// The package name is `scaffold` (not `init`) because `init` is a reserved
// function name in Go and an illegal package name. It lives under
// internal/core/workflow/ alongside the other lifecycle workflows; like them it
// is pure domain logic — it returns data and never writes to stdout/stderr (the
// cli/ layer is the sole writer).
package scaffold

// Branding holds the optional project-branding values collected interactively
// (or via flags) and rendered into workspace/styles.yml.
type Branding struct {
	Title   string
	Tagline string
	Accent  string
}

// Options is the domain input to Scaffold. All string fields are pre-resolved by
// the caller (cli/ layer): there is no flag parsing or directory inspection here.
type Options struct {
	// TargetDir is the directory the project is created in. Empty means the
	// current working directory.
	TargetDir string
	// Name is the project name written into workspace.yml.
	Name string
	// Prefix is the compose/project prefix (default "dwe").
	Prefix string
	// Service is the name of the starter service folder under
	// workspace/services/. Empty means no starter service is scaffolded.
	Service string
	// Branding is the optional styles.yml branding.
	Branding Branding
	// Force overwrites existing files instead of skipping them.
	Force bool
}

// Result reports what Scaffold did. It is returned to the cli/ layer, which
// decides how to present it (text or JSON).
type Result struct {
	// Target is the absolute (or caller-relative) path the project was created in.
	Target string
	// Created lists the project-relative paths that were written.
	Created []string
	// Skipped lists the project-relative paths that already existed and were
	// left untouched (force was not set).
	Skipped []string
	// SymlinkFallback is true when CLAUDE.md could not be symlinked to AGENTS.md
	// and was written as a verbatim copy instead.
	SymlinkFallback bool
	// NestedWarning is true when an ancestor workspace.yml was detected, meaning
	// the new project is being created nested inside an existing one.
	NestedWarning bool
}
