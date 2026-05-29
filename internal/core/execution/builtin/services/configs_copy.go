package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/core/execution/builtin/spec"
)

// ConfigsCopy implements the service_configs_copy builtin: copy service template
// configs into the per-service hub configs/ directory.
type ConfigsCopy struct{}

// Validate checks the with-params for service_configs_copy.
func (ConfigsCopy) Validate(with map[string]any) error {
	service := spec.GetStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_configs_copy: missing required param 'service'")
	}
	mode := spec.GetStringParam(with, "mode", "replace")
	switch mode {
	case "default", "replace", "update":
		return nil
	default:
		return fmt.Errorf("builtin service_configs_copy: unknown mode %q (valid: default, replace, update)", mode)
	}
}

// Describe returns a human-readable plan line for service_configs_copy.
func (ConfigsCopy) Describe(with map[string]any) string {
	service := spec.GetStringParam(with, "service", "")
	mode := spec.GetStringParam(with, "mode", "replace")
	return fmt.Sprintf("builtin: service_configs_copy(service=%s, mode=%s)", service, mode)
}

// Run copies declared configs from configs/services/<svc>/ into <svc.Dir>/configs/.
func (ConfigsCopy) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	serviceName := spec.GetStringParam(with, "service", "")
	mode := spec.GetStringParam(with, "mode", "replace")

	svc, ok := ectx.Config.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found in config", serviceName)
	}
	if svc.Dir == "" {
		return fmt.Errorf("service %q: dir is not set", serviceName)
	}

	// Source: configs/services/<service>/
	srcDir := filepath.Join(ectx.ProjectRoot, "configs", "services", serviceName)
	// Dest: services/<service>/configs/
	destDir := filepath.Join(ectx.ProjectRoot, svc.Dir, "configs")
	svcDir := filepath.Join(ectx.ProjectRoot, svc.Dir)

	for _, entry := range svc.Configs {
		src := filepath.Join(srcDir, entry.File)
		dest := filepath.Join(destDir, entry.File)
		// Guard against path traversal: dest must remain inside destDir.
		cleanDestDir := filepath.Clean(destDir)
		cleanDest := filepath.Clean(dest)
		if cleanDest == cleanDestDir || !strings.HasPrefix(cleanDest, cleanDestDir+string(filepath.Separator)) {
			return fmt.Errorf("service %q: config %q escapes the configs directory", serviceName, entry.File)
		}
		if err := copyConfigFile(src, dest, mode); err != nil {
			return fmt.Errorf("copying %s → %s: %w", src, dest, err)
		}
		ectx.Output.Success(fmt.Sprintf("config %s → %s [%s]", src, dest, mode))

		// If a mountpoint is declared, ensure the file exists at that path
		// (relative to the service dir) so Docker Desktop virtiofs can create
		// a nested file bind mount over it. Touch only — content comes from
		// the bind mount at runtime.
		if entry.Mountpoint != "" {
			mp := filepath.Join(svcDir, entry.Mountpoint)
			if err := touchFile(mp); err != nil {
				return fmt.Errorf("creating mountpoint %s: %w", mp, err)
			}
			ectx.Output.Success(fmt.Sprintf("mountpoint %s [touched]", mp))
		}
	}
	return nil
}

// CopyConfigFile copies src to dest using the given mode:
//   - "default" — skip if dest already exists
//   - "replace" — overwrite unconditionally
//   - "update"  — merge new keys from src into dest without overwriting existing values
//
// The dest directory is created if it does not exist.
// Exported for use in tests.
func CopyConfigFile(src, dest, mode string) error {
	return copyConfigFile(src, dest, mode)
}

func copyConfigFile(src, dest, mode string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	srcData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source %s: %w", src, err)
	}

	switch mode {
	case "default":
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		return os.WriteFile(dest, srcData, 0o644)

	case "replace":
		return os.WriteFile(dest, srcData, 0o644)

	case "update":
		return updateEnvFile(srcData, dest)

	default:
		// Treat unknown mode as "default".
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		return os.WriteFile(dest, srcData, 0o644)
	}
}

// updateEnvFile merges new KEY=VALUE entries from srcData into the dest file.
// Keys already present in dest are preserved unchanged.
func updateEnvFile(srcData []byte, dest string) error {
	destData, err := os.ReadFile(dest)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(dest, srcData, 0o644)
	}
	if err != nil {
		return fmt.Errorf("read dest %s: %w", dest, err)
	}

	existingKeys := spec.ParseEnvKeys(destData)

	var additions []string
	scanner := bufio.NewScanner(strings.NewReader(string(srcData)))
	for scanner.Scan() {
		line := scanner.Text()
		key := spec.EnvLineKey(line)
		if key == "" {
			continue
		}
		if !existingKeys[key] {
			additions = append(additions, line)
		}
	}

	if len(additions) == 0 {
		return nil
	}

	f, err := os.OpenFile(dest, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open dest for append: %w", err)
	}

	var writeErr error
	if len(destData) > 0 && destData[len(destData)-1] != '\n' {
		_, writeErr = f.WriteString("\n")
	}
	for _, line := range additions {
		if writeErr != nil {
			break
		}
		_, writeErr = f.WriteString(line + "\n")
	}

	if closeErr := f.Close(); closeErr != nil && writeErr == nil {
		return closeErr
	}
	return writeErr
}

// touchFile creates an empty file at path (and its parent directories) if it
// does not already exist. If it exists it is left unchanged.
func touchFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}
