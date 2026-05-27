package commands

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/validate"
)

// daemonSentinels maps each model.ErrDaemon* sentinel to the field-marker
// string used for per-field suppression of the fallback model error.
var daemonSentinels = []struct {
	sentinel error
	field    string
}{
	{model.ErrDaemonBlockRequired, "daemon_block"},
	{model.ErrDaemonServiceRequired, "service"},
	{model.ErrDaemonServiceNotLiteral, "service"},
	{model.ErrDaemonContainerTemplateRequired, "container_template"},
	{model.ErrDaemonOnAlreadyRunningInvalid, "on_already_running"},
	{model.ErrDaemonStopTimeoutInvalid, "stop_timeout"},
}

// paramRefRe matches ${param.<name>} references in a template literal.
var paramRefRe = regexp.MustCompile(`\$\{\s*param\.([A-Za-z_][A-Za-z0-9_]*)\s*\}`)

// daemonStructuralDiagnostics emits per-field diagnostics for type=daemon
// commands. It replays the model-level checks so users see file/line/hint
// context at `devbox validate` time, and adds validator-only checks
// (param-reference walks) too rich for the model.
//
// The returned `fields` map records which model sentinels are now redundant
// (their information has already been surfaced richly here). The fallback
// in commands.go uses errors.Is against daemonSentinels and the field map
// to drop only the matching model errors — non-categorised model errors
// (e.g. `cmd is not valid for type=daemon`) still surface.
func daemonStructuralDiagnostics(cmd model.CommandDef, relFile string) ([]validate.Diagnostic, map[string]bool) {
	var out []validate.Diagnostic
	fields := make(map[string]bool)
	target := fmt.Sprintf("commands:%s", cmd.ID)

	// service: required, literal.
	effective := cmd.Service
	if cmd.Runner != nil && cmd.Runner.Service != "" {
		effective = cmd.Runner.Service
	}
	if effective == "" {
		fields["service"] = true
		out = append(out, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  "daemon: service required",
			Hint:     "set service: to a compose service name",
		})
	} else if strings.Contains(effective, "${") || strings.Contains(effective, "{{") {
		fields["service"] = true
		out = append(out, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  "daemon: service must be literal (no ${...} or {{...}})",
			Hint:     "daemon labels need a stable id\n${param.X} / {{...}} are not allowed in v1",
		})
	}

	if cmd.Daemon == nil {
		fields["daemon_block"] = true
		out = append(out, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  "daemon: daemon block required",
			Hint:     "add a daemon: block with container_template:\nsee docs/reference/config/commands.md",
		})
		return out, fields
	}

	if strings.TrimSpace(cmd.Daemon.ContainerTemplate) == "" {
		fields["container_template"] = true
		out = append(out, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  "daemon: container_template required",
			Hint:     "set daemon.container_template to a template literal\nrendered at runtime to produce the container name",
		})
	}

	switch cmd.Daemon.OnAlreadyRunning {
	case "", "error", "noop":
	default:
		fields["on_already_running"] = true
		out = append(out, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  fmt.Sprintf("daemon: on_already_running must be \"error\" or \"noop\" (got %q)", cmd.Daemon.OnAlreadyRunning),
			Hint:     "set daemon.on_already_running to \"error\" or \"noop\"",
		})
	}

	if s := strings.TrimSpace(cmd.Daemon.StopTimeout); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			fields["stop_timeout"] = true
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("daemon: stop_timeout parse %q: %v", s, err),
				Hint:     "accepted forms: \"10s\", \"1m30s\", \"500ms\"\nsub-second values are clamped to 1s by docker stop -t",
			})
		} else if d <= 0 {
			fields["stop_timeout"] = true
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("daemon: stop_timeout must be positive (got %q)", s),
				Hint:     "use a positive duration like \"10s\" or \"500ms\"",
			})
		}
	}

	// Validator-only: every ${param.X} in container_template must reference a
	// declared param, and that param should have a pattern: set. The runtime
	// regex in daemon.ResolveContainerName is the authoritative gate; this
	// surfaces the foot-gun at plan time.
	refs := extractParamRefs(cmd.Daemon.ContainerTemplate)
	for _, ref := range refs {
		pdef, ok := cmd.Params[ref]
		if !ok {
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("daemon: container_template references undeclared param %q", ref),
				Hint:     fmt.Sprintf("declare %q under params: or remove the reference", ref),
			})
			continue
		}
		if strings.TrimSpace(pdef.Pattern) == "" {
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("daemon: container_template references param %q without a pattern:", ref),
				Hint:     fmt.Sprintf("set params.%s.pattern to an anchored regex\nadvisory: runtime regex still gates the rendered name", ref),
			})
		}
	}

	return out, fields
}

// extractParamRefs returns the deduplicated, ordered list of param names
// referenced by ${param.<name>} occurrences in s.
func extractParamRefs(s string) []string {
	matches := paramRefRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		refs = append(refs, m[1])
	}
	return refs
}

// isSuppressedDaemonErr reports whether e matches a daemon sentinel whose
// field has been categorised by daemonStructuralDiagnostics.
func isSuppressedDaemonErr(e error, fields map[string]bool) bool {
	if len(fields) == 0 || e == nil {
		return false
	}
	for _, sf := range daemonSentinels {
		if fields[sf.field] && errors.Is(e, sf.sentinel) {
			return true
		}
	}
	return false
}

// unwrapJoined returns the constituent errors of an errors.Join-produced
// error. For non-joined errors, returns a single-element slice.
func unwrapJoined(err error) []error {
	if err == nil {
		return nil
	}
	if u, ok := err.(interface{ Unwrap() []error }); ok {
		parts := u.Unwrap()
		if len(parts) == 0 {
			return []error{err}
		}
		return parts
	}
	return []error{err}
}
