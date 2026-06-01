package linters

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// builtinAdapters is the single source of truth for the built-in adapter set.
// Adapters are stateless value types, so instances are created once and shared.
func builtinAdapters() map[string]Adapter {
	return map[string]Adapter{
		ShellcheckID: NewShellcheck(),
		HadolintID:   NewHadolint(),
	}
}

// All assembles the set of linter validators for a project. Behavior:
//
//   - validateLoadErr is a non-ErrNotExist error → return zero validators. The
//     `config.validate` validator surfaces the parse error separately;
//     autodetecting linters under a corrupt config could violate explicit
//     `enabled: false` the user thought was in effect.
//   - validateCfg is nil (typically os.ErrNotExist, or defensively a nil-cfg
//     paired with nil err) → synthesize an entry with defaults for every known
//     built-in adapter.
//   - otherwise → bind each user entry to its adapter and, for every built-in
//     adapter without a corresponding user entry, autodetect with defaults.
//
// Reserved-flag rejection short-circuits at binding time: bad user flags
// produce a synthetic error validator instead of registering the linter, so no
// subprocess ever runs for that entry.
func All(validateCfg *config.ValidateConfig, validateLoadErr error, baseDir string, userCfg *userpkg.Config) []validate.Validator {
	if validateLoadErr != nil && !errors.Is(validateLoadErr, os.ErrNotExist) {
		// Return as a top-level DomainLevel+Global validator so that
		// `dwe validate linters [id]` surfaces the error rather than silently
		// producing zero diagnostics. buildRegistry suppresses this when
		// config.validate is already in scope (to avoid a duplicate diagnostic).
		return []validate.Validator{newLinterErrorValidator("_config", 0,
			fmt.Sprintf("workspace/validate.yml: %v", validateLoadErr))}
	}
	children := buildLinterChildren(validateCfg, baseDir, userCfg)
	if len(children) == 0 {
		return nil
	}
	return []validate.Validator{&lintersGroup{children: children}}
}

// buildLinterChildren assembles the per-linter validators that become the
// group's children. Separated from All() so tests can inspect the child set
// without unwrapping the group.
func buildLinterChildren(validateCfg *config.ValidateConfig, baseDir string, userCfg *userpkg.Config) []validate.Validator {
	adapters := builtinAdapters()

	// Deterministic ordering for autodetected built-ins.
	builtinIDs := make([]string, 0, len(adapters))
	for id := range adapters {
		builtinIDs = append(builtinIDs, id)
	}
	sort.Strings(builtinIDs)

	var out []validate.Validator
	configured := make(map[string]struct{})

	if validateCfg != nil {
		for _, entry := range validateCfg.Linters {
			configured[entry.ID] = struct{}{}

			adapter, errValidator := resolveAdapter(entry, adapters)
			if errValidator != nil {
				out = append(out, errValidator)
				continue
			}
			if err := validateUserFlags(adapter, entry.Flags); err != nil {
				out = append(out, newLinterErrorValidator(entry.ID, entry.SourceLine, err.Error()))
				continue
			}
			// Generic adapters have no built-in default paths, so a generic
			// entry without paths: will never match any files — surface this at
			// assembly time instead of silently producing zero diagnostics.
			if entry.Type == "generic" && len(entry.Paths) == 0 {
				out = append(out, newLinterErrorValidator(entry.ID, entry.SourceLine,
					fmt.Sprintf("generic linter %q requires at least one entry in paths: (generic adapters have no built-in defaults)", entry.ID)))
				continue
			}
			out = append(out, newLinterValidator(entry, adapter, baseDir, userCfg))
		}
	}

	// Per-adapter autodetect: for every built-in without an explicit entry,
	// synthesize one with all-default fields.
	for _, id := range builtinIDs {
		if _, has := configured[id]; has {
			continue
		}
		adapter := adapters[id]
		entry := config.LinterEntry{ID: id, Type: "builtin"}
		out = append(out, newLinterValidator(entry, adapter, baseDir, userCfg))
	}

	return out
}

// resolveAdapter binds a configured entry to its Adapter implementation.
// Returns either a non-nil adapter (success) or a non-nil synthetic error
// validator (failure: unknown built-in id). Generic entries always succeed
// because the generic adapter accepts any id.
func resolveAdapter(entry config.LinterEntry, adapters map[string]Adapter) (Adapter, validate.Validator) {
	switch entry.Type {
	case "generic":
		bin := entry.Bin
		if bin == "" {
			bin = entry.ID
		}
		return NewGeneric(entry.ID, bin), nil
	case "", "builtin":
		adapter, ok := adapters[entry.ID]
		if !ok {
			return nil, newLinterErrorValidator(
				entry.ID,
				entry.SourceLine,
				fmt.Sprintf("unknown built-in linter %q (use type: generic for custom binaries)", entry.ID),
			)
		}
		return adapter, nil
	default:
		// LoadValidateConfig already rejects unknown types; defensive fallback.
		return nil, newLinterErrorValidator(
			entry.ID,
			entry.SourceLine,
			fmt.Sprintf("unknown linter type %q", entry.Type),
		)
	}
}

// linterErrorValidator surfaces a load-time problem (unknown built-in id,
// reserved-flag use, etc.) inside the linters domain. It emits exactly one
// Error diagnostic at Run time so the problem appears in the normal
// diagnostics table instead of being lost on the floor.
type linterErrorValidator struct {
	id      string
	line    int
	message string
}

func newLinterErrorValidator(id string, line int, message string) *linterErrorValidator {
	return &linterErrorValidator{id: id, line: line, message: message}
}

func (v *linterErrorValidator) ID() string          { return v.id }
func (v *linterErrorValidator) Domain() string      { return Domain }
func (v *linterErrorValidator) IsDomainLevel() bool { return v.id == "_config" }
func (v *linterErrorValidator) IsGlobal() bool      { return v.id == "_config" }
func (v *linterErrorValidator) Run(_ validate.Context) []validate.Diagnostic {
	return []validate.Diagnostic{{
		Severity: validate.SeverityError,
		Domain:   Domain,
		Target:   v.id,
		File:     "workspace/validate.yml",
		Line:     v.line,
		Message:  v.message,
	}}
}
