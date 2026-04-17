package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// mandatoryDirs are always created for every service hub regardless of config.
// "recreate" mode uses skip semantics for these dirs to avoid removing source
// code or config files by accident.
var mandatoryDirs = []string{"src", "configs"}

type serviceDirsEnsureBuiltin struct{}

func (serviceDirsEnsureBuiltin) Validate(with map[string]any) error {
	service := getStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_dirs_ensure: missing required param 'service'")
	}
	mode := getStringParam(with, "mode", "skip")
	switch mode {
	case "skip", "error", "recreate":
		return nil
	default:
		return fmt.Errorf("builtin service_dirs_ensure: unknown mode %q (valid: skip, error, recreate)", mode)
	}
}

func (serviceDirsEnsureBuiltin) Describe(with map[string]any) string {
	service := getStringParam(with, "service", "")
	mode := getStringParam(with, "mode", "skip")
	return fmt.Sprintf("builtin: service_dirs_ensure(service=%s, mode=%s)", service, mode)
}

func (serviceDirsEnsureBuiltin) Run(with map[string]any, ctx ExecContext) error {
	serviceName := getStringParam(with, "service", "")
	mode := getStringParam(with, "mode", "skip")

	svc, ok := ctx.Config.Services[serviceName]
	if !ok {
		return fmt.Errorf("service_dirs_ensure: service %q not found in config", serviceName)
	}
	if svc.Dir == "" {
		return fmt.Errorf("service_dirs_ensure: service %q: dir is not set", serviceName)
	}

	// Build the full directory list: mandatory first, then configured extras.
	dirs := buildDirList(svc.Dirs)

	// Resolve base directory for the service hub.
	baseDir := filepath.Join(ctx.ProjectRoot, svc.Dir)

	for _, rel := range dirs {
		if err := validateRelDir(rel); err != nil {
			return fmt.Errorf("service_dirs_ensure: service %q: %w", serviceName, err)
		}
		abs := filepath.Join(baseDir, rel)
		// Verify the resolved path stays inside baseDir.
		if err := ensureInsideBase(baseDir, abs); err != nil {
			return fmt.Errorf("service_dirs_ensure: service %q: path %q: %w", serviceName, rel, err)
		}

		isMandatory := isMandatoryDir(rel)
		if err := ensureDir(abs, rel, mode, isMandatory, ctx); err != nil {
			return fmt.Errorf("service_dirs_ensure: service %q: %w", serviceName, err)
		}
	}
	return nil
}

// buildDirList returns the deduplicated list of directories: mandatory first,
// then the configured dirs. Parent-first order is preserved; duplicates removed.
func buildDirList(configuredDirs []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(mandatoryDirs)+len(configuredDirs))
	for _, d := range mandatoryDirs {
		if !seen[d] {
			seen[d] = true
			result = append(result, d)
		}
	}
	for _, d := range configuredDirs {
		if !seen[d] {
			seen[d] = true
			result = append(result, d)
		}
	}
	return result
}

// isMandatoryDir reports whether rel is one of the mandatory dirs.
func isMandatoryDir(rel string) bool {
	return slices.Contains(mandatoryDirs, rel)
}

// validateRelDir checks that rel is a safe relative path (no absolute paths, no ..).
func validateRelDir(rel string) error {
	if rel == "" {
		return fmt.Errorf("dir must not be empty")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("dir %q must be relative (no absolute paths)", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("dir %q is not allowed (root-equivalent or path traversal)", rel)
	}
	return nil
}

// ensureInsideBase verifies that abs is inside baseDir.
func ensureInsideBase(baseDir, abs string) error {
	rel, err := filepath.Rel(baseDir, abs)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path escapes the service directory")
	}
	return nil
}

// ensureDir creates or validates a single directory according to mode.
//
// Mode behaviours:
//
//	skip     — create if missing, no-op if exists as dir, error if exists as non-dir
//	error    — create if missing, error if exists (dir or non-dir)
//	recreate — remove+create if exists as dir, error if exists as non-dir;
//	           mandatory dirs use skip semantics in recreate mode for safety
func ensureDir(abs, rel, mode string, isMandatory bool, ctx ExecContext) error {
	info, err := os.Lstat(abs)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %q: %w", rel, err)
	}

	if exists && !info.IsDir() {
		return fmt.Errorf("path %q exists but is not a directory", rel)
	}

	switch mode {
	case "skip":
		if exists {
			ctx.Output.Info(fmt.Sprintf("dir %s [exists, skipped]", rel))
			return nil
		}
		return createDir(abs, rel, ctx)

	case "error":
		if exists {
			return fmt.Errorf("path %q already exists (mode=error)", rel)
		}
		return createDir(abs, rel, ctx)

	case "recreate":
		// Safety: mandatory dirs use skip semantics even in recreate mode.
		if isMandatory {
			if exists {
				ctx.Output.Info(fmt.Sprintf("dir %s [exists, skipped (mandatory)]", rel))
				return nil
			}
			return createDir(abs, rel, ctx)
		}
		if exists {
			if err := os.RemoveAll(abs); err != nil {
				return fmt.Errorf("removing %q: %w", rel, err)
			}
			ctx.Output.Info(fmt.Sprintf("dir %s [removed]", rel))
		}
		return createDir(abs, rel, ctx)

	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func createDir(abs, rel string, ctx ExecContext) error {
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("creating %q: %w", rel, err)
	}
	ctx.Output.Success(fmt.Sprintf("dir %s [created]", rel))
	return nil
}
