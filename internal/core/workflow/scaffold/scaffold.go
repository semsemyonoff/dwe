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

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/project"
)

// serviceTemplateDir is the embedded service-template directory whose output path
// segment ("app") is rewritten to the chosen Service name (or dropped entirely
// when no starter service is requested).
const serviceTemplateDir = "workspace/services/app"

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

// Scaffold creates a fresh DWE project from opts. It resolves the target
// directory, renders the embedded template plan, writes each file atomically
// (skipping pre-existing files unless opts.Force is set), merges the .gitignore
// block, and links CLAUDE.md to AGENTS.md (with a copy fallback).
//
// It is idempotent: a second run with the same opts leaves every file untouched
// and reports them all as Skipped. It never blocks on a nested project — if an
// ancestor workspace.yml is found, Result.NestedWarning is set and the caller
// decides how to surface it.
func Scaffold(opts Options) (Result, error) {
	target := opts.TargetDir
	if target == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Result{}, fmt.Errorf("scaffold: resolve working directory: %w", err)
		}
		target = cwd
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return Result{}, fmt.Errorf("scaffold: resolve target %s: %w", target, err)
	}

	result := Result{Target: absTarget}

	nested, err := detectNestedProject(absTarget)
	if err != nil {
		return Result{}, err
	}
	result.NestedWarning = nested

	if err := os.MkdirAll(absTarget, dirPerm); err != nil {
		return Result{}, fmt.Errorf("scaffold: create target %s: %w", absTarget, err)
	}

	plan, err := renderPlan(opts)
	if err != nil {
		return Result{}, err
	}
	plan = applyServicePlan(plan, opts.Service)

	for _, rel := range sortedKeys(plan) {
		written, err := writeFile(filepath.Join(absTarget, rel), plan[rel], opts.Force)
		if err != nil {
			return Result{}, err
		}
		if written {
			result.Created = append(result.Created, rel)
		} else {
			result.Skipped = append(result.Skipped, rel)
		}
	}

	// .gitignore is merged (not part of the rendered plan): created when absent,
	// append-merged when present, and a no-op when it already carries the block.
	giPath := filepath.Join(absTarget, ".gitignore")
	giExisted := fileExists(giPath)
	giWritten, err := applyGitignore(giPath)
	if err != nil {
		return Result{}, err
	}
	switch {
	case giWritten && !giExisted:
		result.Created = append(result.Created, ".gitignore")
	case giWritten:
		// Existing file was append-merged; report as created so the change is visible.
		result.Created = append(result.Created, ".gitignore")
	default:
		result.Skipped = append(result.Skipped, ".gitignore")
	}

	// CLAUDE.md mirrors AGENTS.md (symlink, or a copy where symlinks are
	// unavailable). AGENTS.md was just written above, so it is on disk for the
	// copy fallback.
	claudeExisted := fileExists(filepath.Join(absTarget, "CLAUDE.md"))
	fallback, err := linkClaudeMd(absTarget)
	if err != nil {
		return Result{}, err
	}
	result.SymlinkFallback = fallback
	if claudeExisted {
		result.Skipped = append(result.Skipped, "CLAUDE.md")
	} else {
		result.Created = append(result.Created, "CLAUDE.md")
	}

	sort.Strings(result.Created)
	sort.Strings(result.Skipped)
	return result, nil
}

// applyServicePlan rewrites the embedded service-template output paths to the
// chosen service name. When service is empty the starter service is dropped
// entirely; when it differs from the template's "app" segment, the path segment
// is renamed (the file *content* already substitutes [[ .Service ]]).
func applyServicePlan(plan map[string][]byte, service string) map[string][]byte {
	out := make(map[string][]byte, len(plan))
	for path, data := range plan {
		rest, isService := strings.CutPrefix(path, serviceTemplateDir+"/")
		if !isService {
			out[path] = data
			continue
		}
		if service == "" {
			continue
		}
		out["workspace/services/"+service+"/"+rest] = data
	}
	return out
}

// detectNestedProject reports whether any ancestor of target contains a
// workspace.yml — i.e. the new project would be nested inside an existing one.
// The target directory itself is not consulted: a workspace.yml there is a
// re-init (fill-gaps), not a nesting.
func detectNestedProject(target string) (bool, error) {
	dir := filepath.Dir(target)
	for {
		candidate := filepath.Join(dir, project.ConfigFilename)
		if _, err := os.Stat(candidate); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, fmt.Errorf("scaffold: stat %s: %w", candidate, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}

// fileExists reports whether path currently exists (file, dir, or symlink).
func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// sortedKeys returns the plan's output paths in deterministic order, so writes
// (and the resulting Created/Skipped lists) never depend on map iteration order.
func sortedKeys(plan map[string][]byte) []string {
	out := make([]string, 0, len(plan))
	for k := range plan {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
