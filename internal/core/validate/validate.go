package validate

import (
	"context"
	"sort"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/validate/diag"
)

// Severity is the validation diagnostic severity. Re-exported from the leaf
// diag package so loaders that cannot import internal/core/validate (e.g.
// internal/core/project/config, which validate depends on) can still produce diagnostics.
type Severity = diag.Severity

// Severity levels (re-exported from diag).
const (
	SeverityUnknown = diag.SeverityUnknown
	SeverityOK      = diag.SeverityOK
	SeverityInfo    = diag.SeverityInfo
	SeverityWarning = diag.SeverityWarning
	SeverityError   = diag.SeverityError
)

// Diagnostic is a single validation finding. Re-exported from diag.
type Diagnostic = diag.Diagnostic

// Context carries data for validator execution.
type Context struct {
	// Ctx is the parent context for cancellable validator operations (builtin
	// and command runners). Nil is safe; runners fall back to context.Background().
	Ctx             context.Context
	ProjectRoot     string
	ConfigPath      string
	Cfg             *config.DevboxConfig
	CommandRegistry any // *usercommands.Registry; nil-tolerant

	// ValidateCfg is the parsed devbox/validate.yml (nil when the load failed
	// or the file was absent). Single-parse point: the validate command and
	// preflight populate this once; validators and checks.All* read it.
	ValidateCfg *config.ValidateConfig
	// ValidateCfgWarnings carries soft warnings produced by LoadValidateConfig
	// (typically unknown-stage info diagnostics).
	ValidateCfgWarnings []Diagnostic
	// ValidateCfgLoadErr is nil on success, os.ErrNotExist when validate.yml is
	// absent (silently tolerated), or any other error when the load failed.
	ValidateCfgLoadErr error

	// Stage is the lifecycle stage that triggered validation (e.g. "deploy",
	// "run", "stop", "restart", "command"). Empty when invoked outside the
	// preflight hook (i.e. by `devbox validate` directly). Validators can
	// read this to self-skip when their check is irrelevant for the stage
	// (e.g. env.ports_free skips on "stop").
	Stage string
}

// Validator is the interface for domain-specific validators.
type Validator interface {
	ID() string
	Domain() string
	Run(ctx Context) []Diagnostic
}

// DomainLevelValidator is an optional interface for validators that represent
// domain-wide structural errors (e.g. a parse failure that prevents any
// specific entry from loading). When a two-part scope [domain, id] is
// requested, domain-level validators whose domain matches still run — the
// structural error prevents any specific ID from being resolved, so hiding it
// would produce silent zero-diagnostic output.
type DomainLevelValidator interface {
	Validator
	IsDomainLevel() bool
}

// GlobalValidator is an optional interface for validators that must run
// regardless of scope. Used for cross-cutting structural errors (e.g. a
// broken validate.yml) that affect all domains and must be surfaced even when
// the user scopes to a single domain like "env" or "templates".
type GlobalValidator interface {
	Validator
	IsGlobal() bool
}

// GroupValidator is a Validator that owns child validators sharing its
// Domain(). Registry.Run expands children during scope matching and delegates
// execution to RunGroup so the group can choose its own scheduling (parallel,
// ordered, etc.) without each child being individually registered.
//
// The group's own Run is unused — Registry never calls it. Domain()/ID() on
// the group describe the wrapper itself for housekeeping; children carry the
// scope-visible IDs.
type GroupValidator interface {
	Validator
	Children() []Validator
	RunGroup(ctx Context, children []Validator) []Diagnostic
}

// Registry manages and runs validators.
type Registry struct {
	validators []Validator
}

// NewRegistry creates a new validator registry.
func NewRegistry() *Registry {
	return &Registry{validators: []Validator{}}
}

// Register adds a validator to the registry.
func (r *Registry) Register(v Validator) {
	r.validators = append(r.validators, v)
}

// Run executes validators matching the given scope and returns collected diagnostics.
func (r *Registry) Run(ctx Context, scope ...string) []Diagnostic {
	var diags []Diagnostic
	for _, v := range r.validators {
		if gv, ok := v.(GroupValidator); ok {
			var subset []Validator
			for _, c := range gv.Children() {
				if MatchScope(c.Domain(), c.ID(), scope) {
					subset = append(subset, c)
				}
			}
			if len(subset) > 0 {
				diags = append(diags, gv.RunGroup(ctx, subset)...)
			}
			continue
		}
		matches := MatchScope(v.Domain(), v.ID(), scope)
		if !matches {
			if dl, ok := v.(DomainLevelValidator); ok && dl.IsDomainLevel() {
				// Domain-level validators run for any scope that includes their
				// domain, even when a specific ID is requested. The structural
				// error (e.g. a parse failure) prevents any ID from being found.
				matches = len(scope) == 0 || (len(scope) >= 1 && v.Domain() == scope[0])
			}
		}
		if !matches {
			if gv, ok := v.(GlobalValidator); ok && gv.IsGlobal() {
				// Global validators run for every scope — used for cross-cutting
				// structural errors (e.g. broken validate.yml) that must be
				// surfaced even when the user scopes to an unrelated domain.
				matches = true
			}
		}
		if matches {
			diags = append(diags, v.Run(ctx)...)
		}
	}
	sortDiagnostics(diags)
	return diags
}

// MatchScope returns true if the validator matches the requested scope.
// Empty scope matches all validators.
// Single-element scope matches validators in that domain.
// Two-element scope matches validators in that domain with that ID.
func MatchScope(domain, id string, scope []string) bool {
	switch len(scope) {
	case 0:
		return true
	case 1:
		return scope[0] == domain
	default:
		return scope[0] == domain && scope[1] == id
	}
}

func sortDiagnostics(diags []Diagnostic) {
	sort.Slice(diags, func(i, j int) bool {
		di, dj := diags[i], diags[j]
		// Sort by: Severity desc, Domain asc, Target asc, File asc, Line asc
		if di.Severity != dj.Severity {
			return di.Severity > dj.Severity
		}
		if di.Domain != dj.Domain {
			return di.Domain < dj.Domain
		}
		if di.Target != dj.Target {
			return di.Target < dj.Target
		}
		if di.File != dj.File {
			return di.File < dj.File
		}
		return di.Line < dj.Line
	})
}

// Summary aggregates counts of diagnostics by severity.
type Summary struct {
	Errors   int
	Warnings int
	Infos    int
	OKs      int
}

// Aggregate counts diagnostics by severity into a Summary.
func Aggregate(diags []Diagnostic) Summary {
	summary := Summary{}
	for _, d := range diags {
		switch d.Severity {
		case SeverityError:
			summary.Errors++
		case SeverityWarning:
			summary.Warnings++
		case SeverityInfo:
			summary.Infos++
		case SeverityOK:
			summary.OKs++
		default:
			// Treat unrecognized severity as an error so it triggers non-zero exit.
			summary.Errors++
		}
	}
	return summary
}

// ExitCode returns the exit code based on the summary and strict flag.
// Errors always return 1; warnings return 1 only if strict is true.
func ExitCode(summary Summary, strict bool) int {
	if summary.Errors > 0 {
		return 1
	}
	if strict && summary.Warnings > 0 {
		return 1
	}
	return 0
}
