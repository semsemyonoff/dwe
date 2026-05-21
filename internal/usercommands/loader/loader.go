// Package loader discovers and loads command YAML files from a directory.
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"devbox-cli/internal/usercommands/model"
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
	relPath = filepath.ToSlash(relPath)
	relPath = strings.TrimSuffix(relPath, ".yml")
	return strings.Join(strings.Split(relPath, "/"), ".")
}

// ReservedTopLevelIDs lists command IDs that shadow built-in `devbox commands`
// subcommands. A user command whose computed ID equals one of these entries is
// unreachable via `devbox commands <id>` because cobra resolves the subcommand
// first. The validate/commands validator emits a warning when this happens.
var ReservedTopLevelIDs = []string{"list"}

// IsReservedTopLevelID reports whether id exactly matches a reserved top-level
// `devbox commands` subcommand name. Group-qualified ids (e.g. "services.list")
// are NOT reserved.
func IsReservedTopLevelID(id string) bool {
	return slices.Contains(ReservedTopLevelIDs, id)
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

// ParseCommandFile reads and parses a single command YAML file and populates
// the derived metadata fields (FilePath, GroupID, and each CommandDef's ID,
// Group, LocalName) without running cf.Validate(). Callers that want strict
// semantic validation should use LoadCommandFile instead.
//
// The split lets validate/commands emit categorised diagnostics for semantic
// errors that cf.Validate() would otherwise reject up front.
//
// absPath must be an absolute path. baseDir is the commands root directory
// used to derive the relative path for group computation.
func ParseCommandFile(absPath, baseDir string) (*model.CommandFile, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read command file %s: %w", absPath, err)
	}

	cf, err := model.ParseCommandFile(data)
	if err != nil {
		return nil, fmt.Errorf("parse command file %s: %w", absPath, err)
	}

	rel, err := filepath.Rel(baseDir, absPath)
	if err != nil {
		return nil, fmt.Errorf("compute relative path for %s: %w", absPath, err)
	}
	cf.FilePath = absPath
	cf.GroupID = ComputeGroup(rel)

	for name, cmd := range cf.Commands {
		cmd.LocalName = name
		cmd.Group = cf.GroupID
		cmd.ID = ComputeCommandID(cf.GroupID, name)
		cf.Commands[name] = cmd
	}

	return cf, nil
}

// LoadCommandFile reads, parses, and validates a single command YAML file.
// It sets the computed FilePath and GroupID fields and populates each
// CommandDef's computed ID, Group, and LocalName fields.
//
// absPath must be an absolute path. baseDir is the commands root directory
// used to derive the relative path for group computation.
func LoadCommandFile(absPath, baseDir string) (*model.CommandFile, error) {
	cf, err := ParseCommandFile(absPath, baseDir)
	if err != nil {
		return cf, err
	}
	if err := cf.Validate(); err != nil {
		return nil, err
	}
	return cf, nil
}
