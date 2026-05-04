package usercommands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverCommandFiles walks baseDir recursively and returns the absolute paths of
// every *.yml file found. The returned slice is in lexical order.
func DiscoverCommandFiles(baseDir string) ([]string, error) {
	var paths []string
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".yml") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover command files in %s: %w", baseDir, err)
	}
	return paths, nil
}

// ComputeGroup derives the dot-separated group ID from a path relative to the
// commands base directory (e.g. the path passed to DiscoverCommandFiles).
//
// Rules:
//   - Strip the .yml extension.
//   - Replace path separators with dots.
//
// Examples:
//
//	db.yml               → "db"
//	services/main.yml    → "services.main"
//	services/main/db.yml → "services.main.db"
func ComputeGroup(relPath string) string {
	// Normalise separator so we handle both / and OS-specific separators.
	relPath = filepath.ToSlash(relPath)

	// Strip .yml extension.
	relPath = strings.TrimSuffix(relPath, ".yml")

	// Split into segments and join with dots.
	return strings.Join(strings.Split(relPath, "/"), ".")
}

// ComputeCommandID builds the fully-qualified command ID from a group prefix and
// a local command name.
//
// When group is empty (root group) the ID is just the local name.
func ComputeCommandID(group, localName string) string {
	if group == "" {
		return localName
	}
	return group + "." + localName
}

// LoadCommandFile reads, parses, and validates a single command YAML file.
// It sets the computed FilePath and GroupID fields and populates each
// CommandDef's computed ID, Group, and LocalName fields.
//
// absPath must be an absolute path. baseDir is the commands root directory
// used to derive the relative path for group computation.
func LoadCommandFile(absPath, baseDir string) (*CommandFile, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read command file %s: %w", absPath, err)
	}

	cf, err := parseCommandFile(data)
	if err != nil {
		return nil, fmt.Errorf("parse command file %s: %w", absPath, err)
	}

	// Compute relative path and group.
	rel, err := filepath.Rel(baseDir, absPath)
	if err != nil {
		return nil, fmt.Errorf("compute relative path for %s: %w", absPath, err)
	}
	cf.FilePath = absPath
	cf.GroupID = ComputeGroup(rel)

	// Populate computed fields on every CommandDef.
	for name, cmd := range cf.Commands {
		cmd.LocalName = name
		cmd.Group = cf.GroupID
		cmd.ID = ComputeCommandID(cf.GroupID, name)
		cf.Commands[name] = cmd
	}

	// Validate after IDs are set so error messages include the full ID.
	if err := cf.Validate(); err != nil {
		return nil, err
	}

	return cf, nil
}
