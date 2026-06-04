package journal

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

// errSkipSave is a sentinel returned by a mutateState fn to skip the save step.
// Use it to signal "loaded, inspected, nothing changed — no write needed".
var errSkipSave = errors.New("skip save")

// mutateState is the load→mutate→save envelope shared by all pending mutators.
// fn receives the loaded state and may mutate it in place. If fn returns
// errSkipSave the save is skipped and mutateState returns nil. Any other
// non-nil error is returned as-is without saving.
func mutateState(path string, fn func(*ProjectState) error) error {
	state, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		if errors.Is(err, errSkipSave) {
			return nil
		}
		return err
	}
	return Save(path, state)
}

// sortedUniq returns a sorted, deduplicated copy of ss.
// Adjacent duplicate elements (after sorting) are removed.
func sortedUniq(ss []string) []string {
	if len(ss) == 0 {
		return ss
	}
	out := slices.Clone(ss)
	sort.Strings(out)
	return slices.Compact(out)
}

// PendingKind identifies the type of pending operation.
type PendingKind string

// PendingKind constants for the pending operation type.
const (
	PendingKindUnspecified PendingKind = "" // zero value; never stored on a non-nil PendingApply
	PendingRestart         PendingKind = "restart"
	PendingDeploy          PendingKind = "deploy"
)

// PendingOp is a single pending operation of a given kind.
type PendingOp struct {
	Kind     PendingKind `yaml:"kind"`
	Services []string    `yaml:"services,omitempty"` // populated for deploy; empty/ignored for restart
}

// ServiceNames returns a defensive copy of the services list.
func (op *PendingOp) ServiceNames() []string {
	if op == nil {
		return nil
	}
	return slices.Clone(op.Services)
}

// PendingApply tracks outstanding restart/deploy operations after a service toggle.
// Invariant: len(Operations) > 0 whenever the struct is non-nil.
// At most one op per Kind (deduped on insert).
type PendingApply struct {
	Operations []PendingOp `yaml:"operations"`
	CreatedAt  time.Time   `yaml:"created_at,omitempty"`
	ConfigHash string      `yaml:"config_hash"`
}

// Find returns the op for the given kind, or nil if not present.
func (p *PendingApply) Find(kind PendingKind) *PendingOp {
	if p == nil {
		return nil
	}
	for i := range p.Operations {
		if p.Operations[i].Kind == kind {
			return &p.Operations[i]
		}
	}
	return nil
}

// PendingClear describes which ops/services to remove in a ClearPendingOps call.
type PendingClear struct {
	Kind     PendingKind
	Services []string // for PendingDeploy: subset to remove; for PendingRestart: ignored
}

// AddPendingOp loads state from path, merges op into Pending, and saves atomically.
// Rejects PendingKindUnspecified. Creates a fresh state file if one does not exist.
func AddPendingOp(path string, op PendingOp, configHash string) error {
	if op.Kind == PendingKindUnspecified {
		return fmt.Errorf("cannot add pending op with unspecified kind")
	}
	return mutateState(path, func(state *ProjectState) error {
		applyPendingOp(state, op, configHash)
		return nil
	})
}

// AddPendingOps atomically applies all ops in one load+save cycle.
// Rejects if any op has PendingKindUnspecified.
// Empty/nil ops slice is a no-op (no save, no error).
func AddPendingOps(path string, ops []PendingOp, configHash string) error {
	if len(ops) == 0 {
		return nil
	}
	for _, op := range ops {
		if op.Kind == PendingKindUnspecified {
			return fmt.Errorf("cannot add pending op with unspecified kind")
		}
	}
	return mutateState(path, func(state *ProjectState) error {
		for _, op := range ops {
			applyPendingOp(state, op, configHash)
		}
		return nil
	})
}

// ClearPending sets Pending to nil unconditionally and saves.
// Only callers that applied EVERY pending op should use this (e.g. full-project reset).
func ClearPending(path string) error {
	return mutateState(path, func(state *ProjectState) error {
		if state.Pending == nil {
			return errSkipSave
		}
		state.Pending = nil
		return nil
	})
}

// ClearPendingForKind removes every op of the given kind from Operations.
// If Operations becomes empty, sets Pending to nil.
func ClearPendingForKind(path string, kind PendingKind) error {
	return mutateState(path, func(state *ProjectState) error {
		if state.Pending == nil {
			return errSkipSave
		}
		removePendingKind(state, kind)
		return nil
	})
}

// ClearPendingForServices removes named services from the deploy op (or the whole restart op).
// For PendingDeploy: removes the named services; if op.Services becomes empty, removes the op.
// For PendingRestart: services arg is ignored; removes the whole restart op.
// If Operations becomes empty, sets Pending to nil.
func ClearPendingForServices(path string, kind PendingKind, services []string) error {
	return mutateState(path, func(state *ProjectState) error {
		if state.Pending == nil {
			return errSkipSave
		}
		clearPendingForServices(state, kind, services)
		return nil
	})
}

// ClearPendingOps atomically clears multiple ops in one load+save cycle.
// Rejects Kind == PendingKindUnspecified for any entry.
// Empty clears slice is a no-op (no save).
func ClearPendingOps(path string, clears []PendingClear) error {
	if len(clears) == 0 {
		return nil
	}
	for _, c := range clears {
		if c.Kind == PendingKindUnspecified {
			return fmt.Errorf("cannot clear pending op with unspecified kind")
		}
	}
	return mutateState(path, func(state *ProjectState) error {
		if state.Pending == nil {
			return errSkipSave
		}
		for _, c := range clears {
			clearPendingForServices(state, c.Kind, c.Services)
		}
		return nil
	})
}

// ReplaceServiceWithPending atomically removes serviceName from state.Services and
// adds op to Pending in a single load+save cycle. If the state file is missing, the
// remove is a no-op and the pending write still proceeds.
func ReplaceServiceWithPending(path string, serviceName string, op PendingOp, configHash string) error {
	if op.Kind == PendingKindUnspecified {
		return fmt.Errorf("cannot add pending op with unspecified kind")
	}
	return mutateState(path, func(state *ProjectState) error {
		delete(state.Services, serviceName)
		// Recompute aggregates after removal (same as RemoveService does).
		if len(state.Services) == 0 && (state.Project == nil || len(state.Project.Phases) == 0) {
			state.Project = &ProjectLevelState{}
			state.Services = make(map[string]*ServiceState)
		} else {
			Recompute(state)
		}
		applyPendingOp(state, op, configHash)
		return nil
	})
}

// applyPendingOp merges op into state.Pending in memory.
// Ensures at most one op per kind; deduplicates and sorts service names for deploy.
func applyPendingOp(state *ProjectState, op PendingOp, configHash string) {
	if state.Pending == nil {
		state.Pending = &PendingApply{
			CreatedAt:  time.Now().UTC(),
			ConfigHash: configHash,
		}
	}

	existing := state.Pending.Find(op.Kind)
	if existing != nil {
		if op.Kind == PendingDeploy {
			// Merge service names: union, dedup, sort.
			existing.Services = sortedUniq(append(existing.Services, op.Services...))
		}
		// For PendingRestart, no merge needed — restart is stack-wide.
		return
	}
	// New kind: append.
	newOp := PendingOp{Kind: op.Kind}
	if op.Kind == PendingDeploy {
		newOp.Services = sortedUniq(op.Services)
	}
	state.Pending.Operations = append(state.Pending.Operations, newOp)
}

// removePendingKind removes all ops of kind from state.Pending.Operations.
// Sets Pending to nil if Operations becomes empty.
func removePendingKind(state *ProjectState, kind PendingKind) {
	if state.Pending == nil {
		return
	}
	ops := state.Pending.Operations[:0]
	for _, op := range state.Pending.Operations {
		if op.Kind != kind {
			ops = append(ops, op)
		}
	}
	if len(ops) == 0 {
		state.Pending = nil
	} else {
		state.Pending.Operations = ops
	}
}

// clearPendingForServices applies a single PendingClear to state in memory.
func clearPendingForServices(state *ProjectState, kind PendingKind, services []string) {
	if state.Pending == nil {
		return
	}
	if kind == PendingRestart {
		removePendingKind(state, PendingRestart)
		return
	}
	// PendingDeploy: remove named services from the op.
	for i := range state.Pending.Operations {
		if state.Pending.Operations[i].Kind != kind {
			continue
		}
		op := &state.Pending.Operations[i]
		remaining := op.Services[:0]
		removeSet := make(map[string]bool, len(services))
		for _, s := range services {
			removeSet[s] = true
		}
		for _, s := range op.Services {
			if !removeSet[s] {
				remaining = append(remaining, s)
			}
		}
		op.Services = remaining
		if len(op.Services) == 0 {
			// Remove this op entirely.
			state.Pending.Operations = append(
				state.Pending.Operations[:i],
				state.Pending.Operations[i+1:]...,
			)
			if len(state.Pending.Operations) == 0 {
				state.Pending = nil
			}
		}
		return
	}
}
