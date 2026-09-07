package pipeline

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin"
	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// ResolvedStep holds a pipeline step together with the phase it belongs to,
// after when-condition filtering.
//
// Service is non-empty for steps that belong to a per-service deploy pipeline.
// RuntimeWhen is non-nil when the step's When condition is a runtime expression
// (builtin predicate or shell cmd). Such conditions are NOT evaluated at plan-resolution
// time — they are checked immediately before the step executes.
// PhaseWhen carries the phase-level runtime when condition; evaluated once per phase.
// FilesGate is non-nil when the step carries a files_gate directive; evaluated before
// the step executes and independently of RuntimeWhen.
// Timeout is non-zero when the step declares a positive `timeout:`; zero means
// unbounded (absent, "0", or a leaf step whose duration parsed to zero).
type ResolvedStep struct {
	Phase       config.DeployPhase
	Step        config.DeployStep
	Service     string               // non-empty for per-service steps (e.g. "main")
	RuntimeWhen *condition.Condition // step-level runtime when condition; nil otherwise
	PhaseWhen   *condition.Condition // phase-level runtime when condition; nil otherwise
	FilesGate   *filesgate.FilesGate // step-level files gate; nil otherwise
	Parallel    *ResolvedParallel    // non-nil when the step is a parallel group
	Timeout     time.Duration        // step-body timeout; 0 = unbounded
	AutoCheck   bool                 // Step.Check was derived from the `check: auto` sentinel
}

// DisplayCheck returns the plan-output form of the step's `check:`.
//
// An auto check is reported as what the author wrote, not as the machinery it
// resolved to: printing the derived `builtin shell(cmd=! ( … ))` would tell the
// reader to look for a check that is nowhere in their pipeline file. Returns ""
// when the step has no check.
func (rs ResolvedStep) DisplayCheck() string {
	if rs.Step.Check == nil {
		return ""
	}
	if rs.AutoCheck {
		return config.AutoCheckType + " (inverse of when)"
	}
	return FormatAction(rs.Step.Check)
}

// ResolvedParallel is the resolved form of a config.ParallelGroup.
// MaxConcurrent and FailFast carry concrete (defaulted) values; Steps holds the
// resolved sub-steps in declaration order.
type ResolvedParallel struct {
	MaxConcurrent int
	FailFast      bool
	Steps         []ResolvedStep
}

// DisplayPhaseWhen returns the phase-level condition to show in plan output.
//
// PhaseWhen (rendered at resolve time) is preferred over the raw
// Phase.When it was rendered from: displaying the raw form would print the
// literal `${vars.*}` text while execution uses the substituted command —
// the "plan lies about what will run" divergence the resolve-time rendering
// exists to remove. Phase.When is used only when there is no rendered form:
// a template condition (evaluated and filtered at plan time, never stored in
// PhaseWhen), or no condition at all.
func (rs ResolvedStep) DisplayPhaseWhen() *condition.Condition {
	if rs.PhaseWhen != nil {
		return rs.PhaseWhen
	}
	return rs.Phase.When
}

// IsUntracked reports whether this step is excluded from the [N/M] counter
// and its lifecycle output suppressed — true when either the enclosing phase
// or the step itself sets untracked. Failures still surface regardless.
func (rs ResolvedStep) IsUntracked() bool {
	return rs.Phase.Untracked || rs.Step.Untracked
}

// StepAddress returns the full address of a step for display and lookup:
//   - orchestrator steps: "<phase>/<step>"
//   - service steps:      "<service>/<phase>/<step>"
func (rs ResolvedStep) StepAddress() string {
	if rs.Service != "" {
		return rs.Service + "/" + rs.Phase.Name + "/" + rs.Step.Name
	}
	return rs.Phase.Name + "/" + rs.Step.Name
}

// stepBadge returns the display badge for a step based on its type.
func stepBadge(step config.DeployStep) string {
	switch step.Type {
	case "command":
		return "[command]"
	case "builtin":
		return "[builtin]"
	case "dwe":
		return "[dwe]"
	case "shell":
		return "[shell]"
	default:
		return "[" + step.Type + "]"
	}
}

// StepCommand returns the resolved command or action string for plan display.
//   - command: steps — "<dweBin> commands run <id> [--set key=value...]"
//   - builtin: steps — builtin description from registry (e.g. "builtin: confirm(...)")
//   - dwe: steps  — "<dweBin> <args>"
//   - shell: steps   — raw shell command
//
// dweBin is the configured binary name (e.g. "dwe")).
//
// DISPLAY ONLY, and REDACTED: the result is never fed to execution (every
// caller prints it — see the plan printers, buildPlanStepJSON and
// `reset step --dry-run`). Steps are resolved before display, so cmd and with:
// carry the substituted ${vars.*} values, decrypted secrets included. Redaction
// happens on step.Cmd and on the string leaves of a DEEP COPY of step.With
// BEFORE builtin.Describe / the --set formatting run: a value that Describe
// quotes with %q, or that gains escapes on the way, is no longer a substring of
// the registered plaintext and a final pass would miss it. The final
// trace.Redact on the assembled line is the belt-and-braces catch for anything
// reached by another route. The deep copy is load-bearing — With is the same
// map the executor hands to ExecAction (config.DeployStep.Action), and
// cli/lifecycle/reset.go calls StepCommand before the --dry-run branch, so an
// in-place redaction would make a real `dwe reset step <addr>` execute with
// "***" as every secret param.
func StepCommand(step config.DeployStep, dweBin string) string {
	cmd := trace.Redact(step.Cmd)
	with := redactWithValues(step.With)
	switch step.Type {
	case "command":
		parts := []string{dweBin, "commands", "run", strings.TrimSpace(cmd)}
		if len(with) > 0 {
			keys := make([]string, 0, len(with))
			for k := range with {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts = append(parts, "--set", k+"="+fmt.Sprintf("%v", with[k]))
			}
		}
		return trace.Redact(strings.Join(parts, " "))
	case "builtin":
		return trace.Redact(builtin.Describe(cmd, with))
	case "dwe":
		return trace.Redact(dweBin + " " + strings.TrimSpace(cmd))
	case "shell":
		return trace.Redact(strings.TrimSpace(cmd))
	default:
		return trace.Redact(step.Type + ": " + strings.TrimSpace(cmd))
	}
}

// redactWithValues returns a deep copy of a step's with: map with every string
// leaf redacted. Nested maps and slices are copied too, so no part of the
// caller's map is shared with (or mutated by) the display copy.
func redactWithValues(with map[string]any) map[string]any {
	if with == nil {
		return nil
	}
	out := make(map[string]any, len(with))
	for k, v := range with {
		out[k] = redactWithValue(v)
	}
	return out
}

func redactWithValue(v any) any {
	switch t := v.(type) {
	case string:
		return trace.Redact(t)
	case map[string]any:
		return redactWithValues(t)
	case map[any]any:
		out := make(map[any]any, len(t))
		for k, e := range t {
			out[k] = redactWithValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = redactWithValue(e)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, e := range t {
			out[i] = trace.Redact(e)
		}
		return out
	default:
		return v
	}
}

// UnresolvedTemplateRefs returns the distinct ${...} references in s that
// tpl.CompileVarSyntax does not rewrite (tpl.IsVarNamespaceRef), in
// first-occurrence order. Resolve-time rendering (renderStepFields, called from
// ResolvePhaseSteps before a step ever reaches display) substitutes every
// namespace reference in a step's cmd or fails the whole resolve — see
// render.go's renderIfKnown/hasKnownVarRef — so anything still matching
// tpl.VarPattern in StepCommand's output is almost always a genuine leftover (a
// typo or a shell-style ${VAR}), not something the plan failed to substitute.
// Head-only tokens like ${host} are reported for the same reason they are left
// literal: they are shell variables, not references.
//
// Two accepted exceptions this cannot distinguish from a real leftover: a
// resolved ${vars.x} whose substituted *value* happens to itself contain
// literal ${...} text, and a string with no namespace reference at all
// (never entered rendering, e.g. "echo ${HOME}") — both are indistinguishable
// from an unrendered reference by the time StepCommand runs.
func UnresolvedTemplateRefs(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	for _, m := range tpl.VarPattern.FindAllStringSubmatch(s, -1) {
		if tpl.IsVarNamespaceRef(m[1]) {
			continue
		}
		if _, dup := seen[m[0]]; dup {
			continue
		}
		seen[m[0]] = struct{}{}
		out = append(out, m[0])
	}
	return out
}
