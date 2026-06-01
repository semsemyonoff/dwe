// Package registry assembles and queries the command registry.
package registry

import (
	"fmt"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/loader"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
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
	Meta model.GroupMeta
	// Children are direct sub-groups, sorted lexically by Name.
	Children []*GroupNode
	// Commands are the commands that belong directly to this group (not sub-groups).
	// Private commands are included; callers must filter if needed.
	Commands []*model.CommandDef
	// Hidden is the resolved visibility for this group in the current
	// invocation, populated by Registry.ApplyVisibility. Cascades:
	// when a parent is Hidden, every descendant group + command is forced
	// Hidden regardless of its own Meta.Hide expression. Zero value is
	// the safe default when ApplyVisibility has not been called.
	Hidden bool
}

// Registry holds all commands discovered from a commands directory, indexed
// for fast lookup and tree traversal.
type Registry struct {
	// byID maps full command ID to command definition.
	byID map[string]*model.CommandDef
	// groups maps group ID to its GroupNode (includes root "").
	groups map[string]*GroupNode
	// root is the root GroupNode (ID == "").
	root *GroupNode
}

// NewEmptyRegistry returns an empty Registry with no commands.
// Useful as a safe fallback when the commands directory does not exist.
func NewEmptyRegistry() *Registry {
	reg := &Registry{
		byID:   make(map[string]*model.CommandDef),
		groups: make(map[string]*GroupNode),
	}
	reg.root = reg.ensureGroup("")
	return reg
}

// LoadRegistry discovers all command files under baseDir, loads them, and
// assembles a Registry.  It returns an error on any file load failure or
// duplicate command ID.
func LoadRegistry(baseDir string) (*Registry, error) {
	paths, err := loader.DiscoverCommandFiles(baseDir)
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}

	reg := &Registry{
		byID:   make(map[string]*model.CommandDef),
		groups: make(map[string]*GroupNode),
	}

	reg.root = reg.ensureGroup("")

	for _, path := range paths {
		cf, err := loader.LoadCommandFile(path, baseDir)
		if err != nil {
			return nil, fmt.Errorf("load registry: %w", err)
		}

		if err := reg.addCommandFile(cf); err != nil {
			return nil, fmt.Errorf("load registry: %w", err)
		}
	}

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

// addCommandFile inserts every CommandDef in cf into the registry. Daemon
// commands are expanded into virtual .start/.logs/.stop/.restart synthetics
// under a new group node; the source daemon itself is consumed and never
// inserted into byID (so reg.Get("<base>") returns not-found).
//
// Commands are iterated in lexical name order so collision error messages
// are deterministic.
func (r *Registry) addCommandFile(cf *model.CommandFile) error {
	gn := r.ensureGroup(cf.GroupID)
	// Merge per-field: each non-empty value overrides the prior (last-wins
	// per field), while empty fields preserve whatever an earlier file set.
	// Preserves the pre-PR last-wins semantics for Title/Description while
	// extending the same rule to the new Hide field. This avoids the silent
	// regression where a sibling file declaring only `group: {hide: ...}`
	// would wipe a title/description set by an earlier file.
	if cf.Group.Title != "" {
		gn.Meta.Title = cf.Group.Title
	}
	if cf.Group.Description != "" {
		gn.Meta.Description = cf.Group.Description
	}
	if cf.Group.Hide != "" {
		gn.Meta.Hide = cf.Group.Hide
	}

	names := make([]string, 0, len(cf.Commands))
	for n := range cf.Commands {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		cmd := cf.Commands[name]
		if cmd.Type == model.CommandTypeDaemon {
			if err := r.expandAndInsertDaemon(cmd); err != nil {
				return err
			}
			continue
		}
		cmdCopy := cmd
		if existing, dup := r.byID[cmdCopy.ID]; dup {
			return fmt.Errorf("duplicate command ID %q (groups: %s and %s)",
				cmdCopy.ID, existing.Group, cmdCopy.Group)
		}
		r.byID[cmdCopy.ID] = &cmdCopy
		gn.Commands = append(gn.Commands, &cmdCopy)
	}
	return nil
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
		parentID := strings.Join(parts[:len(parts)-1], ".")
		parent := r.ensureGroup(parentID)
		parent.Children = append(parent.Children, gn)
	}
	r.groups[id] = gn
	return gn
}

// AddCommandForTest inserts a CommandDef directly into the registry.
// It is intended only for use in unit tests that need a populated Registry
// without loading YAML files from disk.
func (r *Registry) AddCommandForTest(def *model.CommandDef) {
	r.byID[def.ID] = def
	gn := r.ensureGroup(def.Group)
	gn.Commands = append(gn.Commands, def)
}

// Get returns the CommandDef for the given full command ID.
// Returns an error when the ID is not found.
func (r *Registry) Get(id string) (*model.CommandDef, error) {
	cmd, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("command %q not found", id)
	}
	return cmd, nil
}

// List returns all non-private, non-hidden commands whose group ID starts
// with groupPrefix, sorted lexically by full ID. Pass an empty string to
// list everything matching the visibility filter.
func (r *Registry) List(groupPrefix string) []*model.CommandDef {
	return r.list(groupPrefix, false, false)
}

// ListAll returns all non-hidden commands (including private) whose group ID
// starts with groupPrefix, sorted lexically by full ID. Pass an empty string
// to list everything non-hidden. Hidden commands are never returned from
// ListAll — hide is a runtime "this command does not exist right now" gate,
// distinct from the developer-intent Private flag which --all/ListAll opts in.
//
// To include Hidden commands (e.g. for the inspect-on-hidden debug path that
// lets users discover why a command disappeared), use ListAllIncludingHidden.
func (r *Registry) ListAll(groupPrefix string) []*model.CommandDef {
	return r.list(groupPrefix, true, false)
}

// ListAllIncludingHidden returns every command in the registry whose group ID
// starts with groupPrefix, sorted lexically. Includes both Private and Hidden
// entries — escape hatch for tab-completion in the `dwe commands -i` inspect
// path, where users need to discover hidden IDs to debug their hide: expressions.
// Never use this from public listing, completion, or TUI paths — they must
// honor the runtime visibility filter.
func (r *Registry) ListAllIncludingHidden(groupPrefix string) []*model.CommandDef {
	return r.list(groupPrefix, true, true)
}

func (r *Registry) list(groupPrefix string, includePrivate, includeHidden bool) []*model.CommandDef {
	var result []*model.CommandDef
	for id, cmd := range r.byID {
		if !includeHidden && cmd.Hidden {
			continue
		}
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
func (r *Registry) Validate() error {
	var errs []string
	for _, cmd := range r.byID {
		if cmd.Type != model.CommandTypeWorkflow {
			continue
		}
		WalkWorkflowSteps(cmd.Steps, "step", func(path string, step model.WorkflowStep) {
			if step.Command == "" {
				return
			}
			if _, ok := r.byID[step.Command]; !ok {
				errs = append(errs,
					fmt.Sprintf("command %q %s: references unknown command %q",
						cmd.ID, path, step.Command))
			}
		})
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("registry validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// ValidationIssue represents a single cross-reference validation issue.
type ValidationIssue struct {
	CommandID string // the command ID that has the issue
	// Path is the human-readable location of the issue within the command's
	// step tree. For top-level steps it is "step[i]"; for parallel sub-steps
	// it is "step[i].parallel.steps[j]".
	Path    string
	Message string // the validation issue message
}

// WalkWorkflowSteps recursively visits every step in a workflow tree, including
// sub-steps inside Parallel containers. parentPath is the path prefix WITHOUT
// the trailing index; the root call uses "step".
//
// Each call to visit receives a path of the form parentPath[i] (e.g. "step[0]"
// or "step[2].parallel.steps[1]") and the corresponding step.
func WalkWorkflowSteps(steps []model.WorkflowStep, parentPath string, visit func(path string, step model.WorkflowStep)) {
	for i := range steps {
		step := steps[i]
		path := fmt.Sprintf("%s[%d]", parentPath, i)
		visit(path, step)
		if step.Parallel != nil {
			WalkWorkflowSteps(step.Parallel.Steps, path+".parallel.steps", visit)
		}
	}
}

// Diagnostics returns all cross-reference validation issues found in the registry.
// Returns an empty slice if the registry is valid. The walker recurses into
// Parallel sub-steps so unknown command references inside parallel groups are
// reported with a path-qualified location.
func (r *Registry) Diagnostics() []ValidationIssue {
	var issues []ValidationIssue
	for _, cmd := range r.byID {
		if cmd.Type != model.CommandTypeWorkflow {
			continue
		}
		WalkWorkflowSteps(cmd.Steps, "step", func(path string, step model.WorkflowStep) {
			if step.Command == "" {
				return
			}
			if _, ok := r.byID[step.Command]; !ok {
				issues = append(issues, ValidationIssue{
					CommandID: cmd.ID,
					Path:      path,
					Message:   fmt.Sprintf("references unknown command %q", step.Command),
				})
			}
		})
	}
	// Sort for determinism: by command ID, then by path
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].CommandID != issues[j].CommandID {
			return issues[i].CommandID < issues[j].CommandID
		}
		return issues[i].Path < issues[j].Path
	})
	return issues
}

// BuildRegistryFromParsed builds a registry from already-parsed command files
// without rereading from disk. The parameter type is *model.CommandFile (return type
// of loader.LoadCommandFile).
func BuildRegistryFromParsed(files []*model.CommandFile) (*Registry, error) {
	reg := &Registry{
		byID:   make(map[string]*model.CommandDef),
		groups: make(map[string]*GroupNode),
	}

	reg.root = reg.ensureGroup("")

	// Check for duplicate command IDs across all files
	for _, cf := range files {
		if err := reg.addCommandFile(cf); err != nil {
			return nil, err
		}
	}

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
