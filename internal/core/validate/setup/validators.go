package setup

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/validate"
	"github.com/semsemyonoff/devbox/internal/core/workflow/setup"
)

// Helper functions for creating diagnostics.
func makeError(target, msg string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityError,
		Domain:   "setup",
		Target:   target,
		Message:  msg,
	}
}

func makeWarning(target, msg string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityWarning,
		Domain:   "setup",
		Target:   target,
		Message:  msg,
	}
}

func makeErrorWithHint(target, msg, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityError,
		Domain:   "setup",
		Target:   target,
		Message:  msg,
		Hint:     hint,
	}
}

// Compile-time checks.
var (
	_ validate.Validator = (*parseValidator)(nil)
	_ validate.Validator = (*typeKnownValidator)(nil)
	_ validate.Validator = (*idRequiredValidator)(nil)
	_ validate.Validator = (*idUniqueValidator)(nil)
	_ validate.Validator = (*writesRequiredValidator)(nil)
	_ validate.Validator = (*writesUniqueValidator)(nil)
	_ validate.Validator = (*writesSyntaxValidator)(nil)
	_ validate.Validator = (*writesScopeValidator)(nil)
	_ validate.Validator = (*optionsValidValidator)(nil)
	_ validate.Validator = (*validateExclusiveValidator)(nil)
	_ validate.Validator = (*validateOnlyOnInputValidator)(nil)
	_ validate.Validator = (*validatePresetKnownValidator)(nil)
	_ validate.Validator = (*validateRegexCompilesValidator)(nil)
	_ validate.Validator = (*typeWritesConsistentValidator)(nil)
	_ validate.Validator = (*requiredConsistentValidator)(nil)
)

// parseValidator emits load-error diagnostics when setup.yml fails to parse.
type parseValidator struct {
	err  error
	path string
}

func (v *parseValidator) ID() string     { return "parse" }
func (v *parseValidator) Domain() string { return "setup" }
func (v *parseValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.err == nil || errors.Is(v.err, os.ErrNotExist) {
		return nil
	}
	return []validate.Diagnostic{
		makeError("parse", fmt.Sprintf("failed to parse %s: %v", v.path, v.err)),
	}
}

// typeKnownValidator checks that question types are one of input/select/multiselect/confirm.
type typeKnownValidator struct {
	cfg *setup.Config
}

func (v *typeKnownValidator) ID() string     { return "type_known" }
func (v *typeKnownValidator) Domain() string { return "setup" }
func (v *typeKnownValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	validTypes := map[string]bool{
		setup.TypeInput:       true,
		setup.TypeSelect:      true,
		setup.TypeMultiselect: true,
		setup.TypeConfirm:     true,
	}
	for _, q := range v.cfg.Questions {
		if !validTypes[q.Type] {
			diags = append(diags, makeError("type_known",
				fmt.Sprintf("question %q: invalid type %q (must be one of: input, select, multiselect, confirm)", q.ID, q.Type)))
		}
	}
	return diags
}

// idRequiredValidator checks that every question has a non-empty ID.
type idRequiredValidator struct {
	cfg *setup.Config
}

func (v *idRequiredValidator) ID() string     { return "id_required" }
func (v *idRequiredValidator) Domain() string { return "setup" }
func (v *idRequiredValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for i, q := range v.cfg.Questions {
		if q.ID == "" {
			diags = append(diags, makeError("id_required",
				fmt.Sprintf("question %d: missing or empty id (required for answer mapping)", i)))
		}
	}
	return diags
}

// idUniqueValidator checks that no two questions share the same ID.
type idUniqueValidator struct {
	cfg *setup.Config
}

func (v *idUniqueValidator) ID() string     { return "id_unique" }
func (v *idUniqueValidator) Domain() string { return "setup" }
func (v *idUniqueValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	seen := make(map[string]int)
	var diags []validate.Diagnostic
	for i, q := range v.cfg.Questions {
		if q.ID == "" {
			continue
		}
		if prev, ok := seen[q.ID]; ok {
			diags = append(diags, makeError("id_unique",
				fmt.Sprintf("question id %q is duplicated (first at index %d, again at index %d)", q.ID, prev, i)))
		}
		seen[q.ID] = i
	}
	return diags
}

// writesRequiredValidator checks that every question has a non-empty writes path.
type writesRequiredValidator struct {
	cfg *setup.Config
}

func (v *writesRequiredValidator) ID() string     { return "writes_required" }
func (v *writesRequiredValidator) Domain() string { return "setup" }
func (v *writesRequiredValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Writes == "" {
			diags = append(diags, makeError("writes_required",
				fmt.Sprintf("question %q: missing or empty writes: path (required to record answer in local.yml)", q.ID)))
		}
	}
	return diags
}

// writesUniqueValidator checks that no two questions write to the same path.
type writesUniqueValidator struct {
	cfg *setup.Config
}

func (v *writesUniqueValidator) ID() string     { return "writes_unique" }
func (v *writesUniqueValidator) Domain() string { return "setup" }
func (v *writesUniqueValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	seen := make(map[string]int)
	var diags []validate.Diagnostic
	for i, q := range v.cfg.Questions {
		if q.Writes == "" {
			continue
		}
		if prev, ok := seen[q.Writes]; ok {
			diags = append(diags, makeError("writes_unique",
				fmt.Sprintf("writes path %q is duplicated (first at index %d, again at index %d)", q.Writes, prev, i)))
		}
		seen[q.Writes] = i
		// Reject prefix collisions: a shorter path that is a prefix of (or is prefixed by) an
		// existing path causes setAtPath to silently overwrite an already-built sub-map with a scalar.
		for existing, j := range seen {
			if existing == q.Writes {
				continue // exact duplicate already caught above
			}
			if strings.HasPrefix(q.Writes, existing+".") {
				diags = append(diags, makeError("writes_unique",
					fmt.Sprintf("writes path %q (index %d) is a sub-path of %q (index %d); this would overwrite a nested map with a scalar", existing, j, q.Writes, i)))
			} else if strings.HasPrefix(existing, q.Writes+".") {
				diags = append(diags, makeError("writes_unique",
					fmt.Sprintf("writes path %q (index %d) is a sub-path of %q (index %d); this would overwrite a nested map with a scalar", q.Writes, i, existing, j)))
			}
		}
	}
	return diags
}

// writesSyntaxValidator checks that writes paths are valid dot-paths.
type writesSyntaxValidator struct {
	cfg *setup.Config
}

func (v *writesSyntaxValidator) ID() string     { return "writes_syntax" }
func (v *writesSyntaxValidator) Domain() string { return "setup" }
func (v *writesSyntaxValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Writes == "" {
			continue
		}
		if err := validateWritesPath(q.Writes); err != nil {
			diags = append(diags, makeError("writes_syntax",
				fmt.Sprintf("question %q: invalid writes path %q: %v", q.ID, q.Writes, err)))
		}
	}
	return diags
}

// validateWritesPath checks that a dot-path is syntactically valid.
func validateWritesPath(path string) error {
	if strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") {
		return fmt.Errorf("leading or trailing dot not allowed")
	}
	segments := strings.Split(path, ".")
	for i, seg := range segments {
		if seg == "" {
			return fmt.Errorf("empty path segment at position %d", i)
		}
		if !config.ValidIdentifierKey(seg) {
			return fmt.Errorf("segment %q at position %d is not a valid identifier (must match ^[A-Za-z_][A-Za-z0-9_]*$)", seg, i)
		}
	}
	return nil
}

// writesScopeValidator enforces allowed write targets and shape constraints.
type writesScopeValidator struct {
	cfg *setup.Config
}

func (v *writesScopeValidator) ID() string     { return "writes_scope" }
func (v *writesScopeValidator) Domain() string { return "setup" }
func (v *writesScopeValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	// forbiddenRoots are top-level keys that must not be written by the wizard.
	forbiddenRoots := []string{"info", "styles", "docker"}

	for _, q := range v.cfg.Questions {
		if q.Writes == "" {
			continue
		}

		root := strings.SplitN(q.Writes, ".", 2)[0]
		for _, forbidden := range forbiddenRoots {
			if root == forbidden {
				diags = append(diags, makeError("writes_scope",
					fmt.Sprintf("question %q writes to forbidden namespace %q", q.ID, forbidden)))
				break
			}
		}

		if q.Writes == "services" {
			diags = append(diags, makeError("writes_scope",
				fmt.Sprintf("question %q writes to forbidden namespace %q", q.ID, "services")))
			continue
		}

		if strings.HasPrefix(q.Writes, "services.") {
			if err := validateServiceWritePath(q.Writes); err != nil {
				diags = append(diags, makeError("writes_scope",
					fmt.Sprintf("question %q: %v", q.ID, err)))
				continue
			}
			// Validate that the service name exists in the loaded config.
			if ctx.Cfg != nil {
				parts := strings.SplitN(q.Writes, ".", 3)
				if len(parts) >= 2 {
					svcName := parts[1]
					if _, ok := ctx.Cfg.Services[svcName]; !ok {
						diags = append(diags, makeError("writes_scope",
							fmt.Sprintf("question %q: service %q does not exist", q.ID, svcName)))
					}
				}
			}
		}
	}

	return diags
}

// validateServiceWritePath checks that a services.X.Y.Z path has a valid shape.
func validateServiceWritePath(path string) error {
	parts := strings.Split(path, ".")
	if len(parts) < 3 {
		return fmt.Errorf("services.* path must have at least 3 segments (services.<name>.<key>)")
	}

	key := parts[2]

	switch key {
	case "enabled":
		if len(parts) != 3 {
			return fmt.Errorf("services.*.enabled must be a leaf path")
		}
		return nil
	case "ports":
		if len(parts) != 4 {
			return fmt.Errorf("services.*.ports.<port_name> must be a leaf path (got %d segments)", len(parts))
		}
		return nil
	case "hosts":
		if len(parts) != 4 {
			return fmt.Errorf("services.*.hosts.<host_name> must be a leaf path (got %d segments)", len(parts))
		}
		return nil
	default:
		return fmt.Errorf("services.* only allows enabled, ports.<name>, or hosts.<name>; got services.*.%s", key)
	}
}

// optionsValidValidator checks that select/multiselect questions have valid options.
type optionsValidValidator struct {
	cfg *setup.Config
}

func (v *optionsValidValidator) ID() string     { return "options_valid" }
func (v *optionsValidValidator) Domain() string { return "setup" }
func (v *optionsValidValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Type == setup.TypeSelect || q.Type == setup.TypeMultiselect {
			if len(q.Options) == 0 {
				diags = append(diags, makeError("options_valid",
					fmt.Sprintf("question %q: %s requires non-empty options", q.ID, q.Type)))
				continue
			}

			seen := make(map[string]bool)
			for i, opt := range q.Options {
				if opt.Value == "" {
					diags = append(diags, makeError("options_valid",
						fmt.Sprintf("question %q: option %d has empty value (empty value collides with no-answer zero-value)", q.ID, i)))
				}
				if seen[opt.Value] {
					diags = append(diags, makeError("options_valid",
						fmt.Sprintf("question %q: option value %q is duplicated", q.ID, opt.Value)))
				}
				seen[opt.Value] = true
			}
		}
	}
	return diags
}

// validateExclusiveValidator checks that preset and regex are not both set.
type validateExclusiveValidator struct {
	cfg *setup.Config
}

func (v *validateExclusiveValidator) ID() string     { return "validate_exclusive" }
func (v *validateExclusiveValidator) Domain() string { return "setup" }
func (v *validateExclusiveValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Validate != nil && q.Validate.Preset != "" && q.Validate.Regex != "" {
			diags = append(diags, makeError("validate_exclusive",
				fmt.Sprintf("question %q: preset and regex cannot both be set", q.ID)))
		}
	}
	return diags
}

// validateOnlyOnInputValidator checks that preset and regex are only used on input types.
type validateOnlyOnInputValidator struct {
	cfg *setup.Config
}

func (v *validateOnlyOnInputValidator) ID() string     { return "validate_only_on_input" }
func (v *validateOnlyOnInputValidator) Domain() string { return "setup" }
func (v *validateOnlyOnInputValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Validate == nil {
			continue
		}
		if q.Type != setup.TypeInput {
			if q.Validate.Preset != "" {
				diags = append(diags, makeError("validate_only_on_input",
					fmt.Sprintf("question %q: preset validation is only meaningful for type: input (found type: %s)", q.ID, q.Type)))
			}
			if q.Validate.Regex != "" {
				diags = append(diags, makeError("validate_only_on_input",
					fmt.Sprintf("question %q: regex validation is only meaningful for type: input (found type: %s)", q.ID, q.Type)))
			}
		}
	}
	return diags
}

// validatePresetKnownValidator checks that preset values are recognized.
type validatePresetKnownValidator struct {
	cfg *setup.Config
}

func (v *validatePresetKnownValidator) ID() string     { return "validate_preset_known" }
func (v *validatePresetKnownValidator) Domain() string { return "setup" }
func (v *validatePresetKnownValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	validPresets := map[string]bool{
		setup.PresetPort:     true,
		setup.PresetHostname: true,
		setup.PresetPath:     true,
		setup.PresetNonEmpty: true,
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Validate != nil && q.Validate.Preset != "" && !validPresets[q.Validate.Preset] {
			diags = append(diags, makeErrorWithHint("validate_preset_known",
				fmt.Sprintf("question %q: unknown preset %q (supported: port, hostname, path, non-empty)", q.ID, q.Validate.Preset),
				"supported: port, hostname, path, non-empty"))
		}
	}
	return diags
}

// validateRegexCompilesValidator checks that regex patterns compile.
type validateRegexCompilesValidator struct {
	cfg *setup.Config
}

func (v *validateRegexCompilesValidator) ID() string     { return "validate_regex_compiles" }
func (v *validateRegexCompilesValidator) Domain() string { return "setup" }
func (v *validateRegexCompilesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Validate != nil && q.Validate.Regex != "" {
			if _, err := regexp.Compile(q.Validate.Regex); err != nil {
				diags = append(diags, makeError("validate_regex_compiles",
					fmt.Sprintf("question %q: regex pattern fails to compile: %v", q.ID, err)))
			}
		}
	}
	return diags
}

// typeWritesConsistentValidator checks that question type produces compatible values for the writes target.
type typeWritesConsistentValidator struct {
	cfg *setup.Config
}

func (v *typeWritesConsistentValidator) ID() string     { return "type_writes_consistent" }
func (v *typeWritesConsistentValidator) Domain() string { return "setup" }
func (v *typeWritesConsistentValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Writes == "" {
			continue
		}

		if strings.HasPrefix(q.Writes, "services.") {
			diags = append(diags, v.checkServiceWrites(q)...)
		}
	}
	return diags
}

func (v *typeWritesConsistentValidator) checkServiceWrites(q setup.Question) []validate.Diagnostic {
	var diags []validate.Diagnostic
	parts := strings.Split(q.Writes, ".")
	if len(parts) < 3 {
		return diags
	}

	key := parts[2]
	switch key {
	case "enabled":
		if q.Type != setup.TypeConfirm {
			diags = append(diags, makeError("type_writes_consistent",
				fmt.Sprintf("question %q writes to services.*.enabled which requires type: confirm (found type: %s)", q.ID, q.Type)))
		}
	case "ports":
		if q.Type != setup.TypeInput || q.Validate == nil || q.Validate.Preset != setup.PresetPort {
			diags = append(diags, makeError("type_writes_consistent",
				fmt.Sprintf("question %q writes to services.*.ports.* which requires type: input with validate.preset: port (found type: %s)", q.ID, q.Type)))
		}
	case "hosts":
		if q.Type != setup.TypeInput {
			diags = append(diags, makeError("type_writes_consistent",
				fmt.Sprintf("question %q writes to services.*.hosts.* which requires type: input (found type: %s)", q.ID, q.Type)))
		}
	}
	return diags
}

// requiredConsistentValidator checks that required is consistent with question type.
type requiredConsistentValidator struct {
	cfg *setup.Config
}

func (v *requiredConsistentValidator) ID() string     { return "required_consistent" }
func (v *requiredConsistentValidator) Domain() string { return "setup" }
func (v *requiredConsistentValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, q := range v.cfg.Questions {
		if q.Type == setup.TypeConfirm && q.Required {
			diags = append(diags, makeWarning("required_consistent",
				fmt.Sprintf("question %q: type: confirm ignores required (confirm always has a value: true or false)", q.ID)))
		}
	}
	return diags
}
