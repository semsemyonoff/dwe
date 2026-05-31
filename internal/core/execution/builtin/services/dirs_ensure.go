package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/devbox/internal/core/execution/builtin/spec"
)

// mandatoryDirs are always created for every service hub regardless of config.
// "recreate" mode uses skip semantics for these dirs to avoid removing source
// code by accident. The configs/ directory is created lazily by
// service_configs_copy when configs: entries are declared on the service.
var mandatoryDirs = []string{"src"}

// DirsEnsure implements the service_dirs_ensure builtin: create the mandatory
// and configured directories under the per-service hub directory.
type DirsEnsure struct{}

// Validate checks the with-params for service_dirs_ensure.
func (DirsEnsure) Validate(with map[string]any) error {
	service := spec.GetStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_dirs_ensure: missing required param 'service'")
	}
	mode := spec.GetStringParam(with, "mode", "skip")
	switch mode {
	case "skip", "error", "recreate":
		return nil
	default:
		return fmt.Errorf("builtin service_dirs_ensure: unknown mode %q (valid: skip, error, recreate)", mode)
	}
}

// Describe returns a human-readable plan line for service_dirs_ensure.
func (DirsEnsure) Describe(with map[string]any) string {
	service := spec.GetStringParam(with, "service", "")
	mode := spec.GetStringParam(with, "mode", "skip")
	return fmt.Sprintf("builtin: service_dirs_ensure(service=%s, mode=%s)", service, mode)
}

// Run creates the mandatory and configured directories per the chosen mode.
func (DirsEnsure) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	serviceName := spec.GetStringParam(with, "service", "")
	mode := spec.GetStringParam(with, "mode", "skip")

	svc, ok := ectx.Config.Services[serviceName]
	if !ok {
		return fmt.Errorf("service_dirs_ensure: service %q not found in config", serviceName)
	}
	if svc.Dir == "" {
		return fmt.Errorf("service_dirs_ensure: service %q: dir is not set", serviceName)
	}

	// Build the full directory list: mandatory first, then configured extras.
	dirs := buildDirList(svc.Dirs)

	// Resolve base directory for the service hub.
	baseDir := filepath.Join(ectx.ProjectRoot, svc.Dir)

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
		if err := ensureDir(abs, rel, mode, isMandatory, ectx); err != nil {
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
//	           the mandatory src/ dir uses skip semantics in recreate mode for safety
func ensureDir(abs, rel, mode string, isMandatory bool, ectx spec.ExecContext) error {
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
			ectx.Output.Info(fmt.Sprintf("dir %s [exists, skipped]", rel))
			return nil
		}
		return createDir(abs, rel, ectx)

	case "error":
		if exists {
			return fmt.Errorf("path %q already exists (mode=error)", rel)
		}
		return createDir(abs, rel, ectx)

	case "recreate":
		// Safety: mandatory dirs use skip semantics even in recreate mode.
		if isMandatory {
			if exists {
				ectx.Output.Info(fmt.Sprintf("dir %s [exists, skipped (mandatory)]", rel))
				return nil
			}
			return createDir(abs, rel, ectx)
		}
		if exists {
			if err := os.RemoveAll(abs); err != nil {
				return fmt.Errorf("removing %q: %w", rel, err)
			}
			ectx.Output.Info(fmt.Sprintf("dir %s [removed]", rel))
		}
		return createDir(abs, rel, ectx)

	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func createDir(abs, rel string, ectx spec.ExecContext) error {
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("creating %q: %w", rel, err)
	}
	ectx.Output.Success(fmt.Sprintf("dir %s [created]", rel))
	return nil
}
