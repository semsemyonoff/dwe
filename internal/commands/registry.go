package commands

import (
	"fmt"
	"sort"
	"strings"
)

// GroupNode is a node in the command group tree.
type GroupNode struct {
	// ID is the full dot-separated group ID (e.g. "services.main").
	// Empty string means the root group.
	ID string
	// Name is the last segment of the group ID (e.g. "main").
	// Empty string for the root group.
	Name string
	// Meta holds optional group metadata declared in the command file.
	Meta GroupMeta
	// Children are direct sub-groups, sorted lexically by Name.
	Children []*GroupNode
	// Commands are the commands that belong directly to this group (not sub-groups).
	// Private commands are included; callers must filter if needed.
	Commands []*CommandDef
}

// Registry holds all commands discovered from a commands directory, indexed
// for fast lookup and tree traversal.
type Registry struct {
	// byID maps full command ID to command definition.
	byID map[string]*CommandDef
	// groups maps group ID to its GroupNode (includes root "").
	groups map[string]*GroupNode
	// root is the root GroupNode (ID == "").
	root *GroupNode
}

// NewEmptyRegistry returns an empty Registry with no commands.
// Useful as a safe fallback when the commands directory does not exist.
func NewEmptyRegistry() *Registry {
	reg := &Registry{
		byID:   make(map[string]*CommandDef),
		groups: make(map[string]*GroupNode),
	}
	reg.root = reg.ensureGroup("")
	return reg
}

// LoadRegistry discovers all command files under baseDir, loads them, and
// assembles a Registry.  It returns an error on any file load failure or
// duplicate command ID.
func LoadRegistry(baseDir string) (*Registry, error) {
	paths, err := DiscoverCommandFiles(baseDir)
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}

	reg := &Registry{
		byID:   make(map[string]*CommandDef),
		groups: make(map[string]*GroupNode),
	}

	// Ensure root node exists.
	reg.root = reg.ensureGroup("")

	for _, path := range paths {
		cf, err := LoadCommandFile(path, baseDir)
		if err != nil {
			return nil, fmt.Errorf("load registry: %w", err)
		}

		// Ensure the group node exists and set its metadata.
		gn := reg.ensureGroup(cf.GroupID)
		// Only set metadata when the file declares a group block with content.
		if cf.Group.Title != "" || cf.Group.Description != "" {
			gn.Meta = cf.Group
		}

		// Register each command.
		for name := range cf.Commands {
			cmd := cf.Commands[name]
			cmdCopy := cmd // take address of local copy
			if existing, dup := reg.byID[cmdCopy.ID]; dup {
				return nil, fmt.Errorf("load registry: duplicate command ID %q (files: %s and %s)",
					cmdCopy.ID, existing.Group, cmdCopy.Group)
			}
			reg.byID[cmdCopy.ID] = &cmdCopy
			gn.Commands = append(gn.Commands, &cmdCopy)
		}
	}

	// Sort commands within each group for deterministic output.
	for _, gn := range reg.groups {
		sort.Slice(gn.Commands, func(i, j int) bool {
			return gn.Commands[i].LocalName < gn.Commands[j].LocalName
		})
		sort.Slice(gn.Children, func(i, j int) bool {
			return gn.Children[i].Name < gn.Children[j].Name
		})
	}

	return reg, nil
}

// ensureGroup returns the GroupNode for the given dot-separated group ID,
// creating it (and all ancestor nodes) if necessary.
func (r *Registry) ensureGroup(id string) *GroupNode {
	if gn, ok := r.groups[id]; ok {
		return gn
	}
	gn := &GroupNode{ID: id}
	if id == "" {
		gn.Name = ""
	} else {
		parts := strings.Split(id, ".")
		gn.Name = parts[len(parts)-1]
		// Ensure parent exists and attach this node as a child.
		parentID := strings.Join(parts[:len(parts)-1], ".")
		parent := r.ensureGroup(parentID)
		parent.Children = append(parent.Children, gn)
	}
	r.groups[id] = gn
	return gn
}

// Get returns the CommandDef for the given full command ID.
// Returns an error when the ID is not found.
func (r *Registry) Get(id string) (*CommandDef, error) {
	cmd, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("command %q not found", id)
	}
	return cmd, nil
}

// List returns all non-private commands whose group ID starts with groupPrefix,
// sorted lexically by full ID.  Pass an empty string to list all non-private commands.
func (r *Registry) List(groupPrefix string) []*CommandDef {
	return r.list(groupPrefix, false)
}

// ListAll returns all commands (including private) whose group ID starts with
// groupPrefix, sorted lexically by full ID.  Pass an empty string to list everything.
func (r *Registry) ListAll(groupPrefix string) []*CommandDef {
	return r.list(groupPrefix, true)
}

func (r *Registry) list(groupPrefix string, includePrivate bool) []*CommandDef {
	var result []*CommandDef
	for id, cmd := range r.byID {
		if !includePrivate && cmd.Private {
			continue
		}
		if groupPrefix == "" || id == groupPrefix ||
			strings.HasPrefix(id, groupPrefix+".") {
			result = append(result, cmd)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// Groups returns the root GroupNode of the command group tree.
// The tree is built from the groups encountered during loading.
func (r *Registry) Groups() *GroupNode {
	return r.root
}

// Validate performs cross-registry validation:
//   - Workflow steps that reference a command ID must exist in the registry.
//   - Workflow steps that reference a private command ID are allowed (private
//     commands are intended to be called from workflows).
//
// It does NOT validate service names against the devbox config; that validation
// requires config context and is performed at execution time.
func (r *Registry) Validate() error {
	var errs []string
	for _, cmd := range r.byID {
		if cmd.Type != CommandTypeWorkflow {
			continue
		}
		for i, step := range cmd.Steps {
			if step.Command == "" {
				continue // confirm steps have no command reference
			}
			if _, ok := r.byID[step.Command]; !ok {
				errs = append(errs,
					fmt.Sprintf("command %q step[%d]: references unknown command %q",
						cmd.ID, i, step.Command))
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("registry validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
