package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
)

// resolveAgentsTemplatePack resolves a template pack directory for a service.
// Returns the absolute path to a directory under devbox/templates/agents/.
// Explicit is strict: if svc.AIDocs.Template is set and does not exist, returns an error.
// Implicit chain: service-name → default, with fallthrough only on ErrNotExist.
func resolveAgentsTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}

	// Validate template key and service name
	if err := validateIDETemplateKey(svc.AIDocs.Template); err != nil {
		return "", fmt.Errorf("invalid ai_docs.template %q: %w", svc.AIDocs.Template, err)
	}
	if err := validateServiceNameAsPackKey(serviceName); err != nil {
		return "", fmt.Errorf("service name cannot be used as implicit template pack key: %w", err)
	}

	// Explicit candidate (strict — hard error on any condition, including not-found; never falls through)
	if svc.AIDocs.Template != "" {
		candidate := filepath.Join(absRoot, "devbox", "templates", "agents", svc.AIDocs.Template)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Pack exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("agents template pack %q is a symlink; symlinked packs are not supported", svc.AIDocs.Template)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("agents template pack %q is not a directory", svc.AIDocs.Template)
			}
			return candidate, nil
		}
		// Any error other than not-exists is a hard error
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat agents template pack %q: %w", svc.AIDocs.Template, err)
		}
		// ErrNotExist with explicit template: strict error, no fallthrough
		return "", fmt.Errorf("agents template pack %q not found (required by explicit ai_docs.template setting)", svc.AIDocs.Template)
	}

	// Implicit chain: service-name → default
	candidates := []string{serviceName, "default"}
	for _, name := range candidates {
		candidate := filepath.Join(absRoot, "devbox", "templates", "agents", name)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Candidate exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("agents template pack %q is a symlink; symlinked packs are not supported", name)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("agents template pack %q is not a directory", name)
			}
			return candidate, nil
		}
		// Only ErrNotExist advances to next candidate; any other error is hard
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat agents template pack %q: %w", name, err)
		}
		// ErrNotExist: continue to next candidate
	}

	// No pack found in implicit chain
	return "", fmt.Errorf("agents template pack not found (tried %s, default): %w", serviceName, os.ErrNotExist)
}
