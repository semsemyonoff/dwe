package builtin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type serviceConfigsCopyBuiltin struct{}

func (serviceConfigsCopyBuiltin) Validate(with map[string]any) error {
	service := getStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_configs_copy: missing required param 'service'")
	}
	mode := getStringParam(with, "mode", "replace")
	switch mode {
	case "default", "replace", "update":
		return nil
	default:
		return fmt.Errorf("builtin service_configs_copy: unknown mode %q (valid: default, replace, update)", mode)
	}
}

func (serviceConfigsCopyBuiltin) Describe(with map[string]any) string {
	service := getStringParam(with, "service", "")
	mode := getStringParam(with, "mode", "replace")
	return fmt.Sprintf("builtin: service_configs_copy(service=%s, mode=%s)", service, mode)
}

func (serviceConfigsCopyBuiltin) Run(_ context.Context, with map[string]any, ectx ExecContext) error {
	serviceName := getStringParam(with, "service", "")
	mode := getStringParam(with, "mode", "replace")

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

	existingKeys := parseEnvKeys(destData)

	var additions []string
	scanner := bufio.NewScanner(strings.NewReader(string(srcData)))
	for scanner.Scan() {
		line := scanner.Text()
		key := envLineKey(line)
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

// ParseEnvKeys returns a set of KEY names found in env file content.
// Exported for use in tests.
func ParseEnvKeys(data []byte) map[string]bool { return parseEnvKeys(data) }

func parseEnvKeys(data []byte) map[string]bool {
	keys := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if key := envLineKey(scanner.Text()); key != "" {
			keys[key] = true
		}
	}
	return keys
}

// EnvLineKey returns the KEY part of a "KEY=VALUE" env line.
// Returns "" for blank lines and comment lines.
// Exported for use in tests.
func EnvLineKey(line string) string { return envLineKey(line) }

func envLineKey(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	key, _, _ := strings.Cut(line, "=")
	return strings.TrimSpace(key)
}
