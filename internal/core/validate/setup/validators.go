package setup

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/core/workflow/setup"
)

// baseValidator carries the shared ID/Domain plus the diagnostic constructors.
// Every setup diagnostic's Target equals the validator's own ID, so the
// constructors read it from the embedded base instead of taking it as an arg.
type baseValidator struct {
	id string
}

func (b baseValidator) ID() string     { return b.id }
func (b baseValidator) Domain() string { return "setup" }

func (b baseValidator) makeError(msg string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityError,
		Domain:   "setup",
		Target:   b.id,
		Message:  msg,
	}
}

func (b baseValidator) makeWarning(msg string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityWarning,
		Domain:   "setup",
		Target:   b.id,
		Message:  msg,
	}
}

func (b baseValidator) makeErrorWithHint(msg, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityError,
		Domain:   "setup",
		Target:   b.id,
		Message:  msg,
		Hint:     hint,
	}
}

// cfgValidator is the base for the question-driven validators. questions()
// folds the nil-config guard into iteration: a nil config yields no questions,
// so each Run can range over questions() without a separate guard.
type cfgValidator struct {
	baseValidator
	cfg *setup.Config
}

func (v cfgValidator) questions() []setup.Question {
	if v.cfg == nil {
		return nil
	}
	return v.cfg.Questions
}

// newCfg builds a cfgValidator base with the given diagnostic ID.
func newCfg(id string, cfg *setup.Config) cfgValidator {
	return cfgValidator{baseValidator: baseValidator{id: id}, cfg: cfg}
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
	baseValidator
	err  error
	path string
}

func (v *parseValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if v.err == nil || errors.Is(v.err, os.ErrNotExist) {
		return nil
	}
	return []validate.Diagnostic{
		v.makeError(fmt.Sprintf("failed to parse %s: %v", v.path, v.err)),
	}
}

// typeKnownValidator checks that question types are one of input/select/multiselect/confirm.
type typeKnownValidator struct {
	cfgValidator
}

func (v *typeKnownValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	validTypes := map[string]bool{
		setup.TypeInput:       true,
		setup.TypeSelect:      true,
		setup.TypeMultiselect: true,
		setup.TypeConfirm:     true,
	}
	for _, q := range v.questions() {
		if !validTypes[q.Type] {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %q: invalid type %q (must be one of: input, select, multiselect, confirm)", q.ID, q.Type)))
		}
	}
	return diags
}

// idRequiredValidator checks that every question has a non-empty ID.
type idRequiredValidator struct {
	cfgValidator
}

func (v *idRequiredValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for i, q := range v.questions() {
		if q.ID == "" {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %d: missing or empty id (required for answer mapping)", i)))
		}
	}
	return diags
}

// idUniqueValidator checks that no two questions share the same ID.
type idUniqueValidator struct {
	cfgValidator
}

func (v *idUniqueValidator) Run(ctx validate.Context) []validate.Diagnostic {
	seen := make(map[string]int)
	var diags []validate.Diagnostic
	for i, q := range v.questions() {
		if q.ID == "" {
			continue
		}
		if prev, ok := seen[q.ID]; ok {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question id %q is duplicated (first at index %d, again at index %d)", q.ID, prev, i)))
		}
		seen[q.ID] = i
	}
	return diags
}

// writesRequiredValidator checks that every question has a non-empty writes path.
type writesRequiredValidator struct {
	cfgValidator
}

func (v *writesRequiredValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Writes == "" {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %q: missing or empty writes: path (required to record answer in local.yml)", q.ID)))
		}
	}
	return diags
}

// writesUniqueValidator checks that no two questions write to the same path.
type writesUniqueValidator struct {
	cfgValidator
}

func (v *writesUniqueValidator) Run(ctx validate.Context) []validate.Diagnostic {
	seen := make(map[string]int)
	var diags []validate.Diagnostic
	for i, q := range v.questions() {
		if q.Writes == "" {
			continue
		}
		if prev, ok := seen[q.Writes]; ok {
			diags = append(diags, v.makeError(
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
				diags = append(diags, v.makeError(
					fmt.Sprintf("writes path %q (index %d) is a sub-path of %q (index %d); this would overwrite a nested map with a scalar", existing, j, q.Writes, i)))
			} else if strings.HasPrefix(existing, q.Writes+".") {
				diags = append(diags, v.makeError(
					fmt.Sprintf("writes path %q (index %d) is a sub-path of %q (index %d); this would overwrite a nested map with a scalar", q.Writes, i, existing, j)))
			}
		}
	}
	return diags
}

// writesSyntaxValidator checks that writes paths are valid dot-paths.
type writesSyntaxValidator struct {
	cfgValidator
}

func (v *writesSyntaxValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Writes == "" {
			continue
		}
		if err := validateWritesPath(q.Writes); err != nil {
			diags = append(diags, v.makeError(
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
	cfgValidator
}

func (v *writesScopeValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	// forbiddenRoots are top-level keys that must not be written by the wizard.
	forbiddenRoots := []string{"info", "styles", "docker"}

	for _, q := range v.questions() {
		if q.Writes == "" {
			continue
		}

		root, _, _ := strings.Cut(q.Writes, ".")
		for _, forbidden := range forbiddenRoots {
			if root == forbidden {
				diags = append(diags, v.makeError(
					fmt.Sprintf("question %q writes to forbidden namespace %q", q.ID, forbidden)))
				break
			}
		}

		if q.Writes == "services" {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %q writes to forbidden namespace %q", q.ID, "services")))
			continue
		}

		if strings.HasPrefix(q.Writes, "services.") {
			if err := validateServiceWritePath(q.Writes); err != nil {
				diags = append(diags, v.makeError(
					fmt.Sprintf("question %q: %v", q.ID, err)))
				continue
			}
			// Validate that the service name exists in the loaded config.
			if ctx.Cfg != nil {
				parts := strings.SplitN(q.Writes, ".", 3)
				if len(parts) >= 2 {
					svcName := parts[1]
					if _, ok := ctx.Cfg.Services[svcName]; !ok {
						diags = append(diags, v.makeError(
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
	cfgValidator
}

func (v *optionsValidValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Type == setup.TypeSelect || q.Type == setup.TypeMultiselect {
			if len(q.Options) == 0 {
				diags = append(diags, v.makeError(
					fmt.Sprintf("question %q: %s requires non-empty options", q.ID, q.Type)))
				continue
			}

			seen := make(map[string]bool)
			for i, opt := range q.Options {
				if opt.Value == "" {
					diags = append(diags, v.makeError(
						fmt.Sprintf("question %q: option %d has empty value (empty value collides with no-answer zero-value)", q.ID, i)))
				}
				if seen[opt.Value] {
					diags = append(diags, v.makeError(
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
	cfgValidator
}

func (v *validateExclusiveValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Validate != nil && q.Validate.Preset != "" && q.Validate.Regex != "" {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %q: preset and regex cannot both be set", q.ID)))
		}
	}
	return diags
}

// validateOnlyOnInputValidator checks that preset and regex are only used on input types.
type validateOnlyOnInputValidator struct {
	cfgValidator
}

func (v *validateOnlyOnInputValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Validate == nil {
			continue
		}
		if q.Type != setup.TypeInput {
			if q.Validate.Preset != "" {
				diags = append(diags, v.makeError(
					fmt.Sprintf("question %q: preset validation is only meaningful for type: input (found type: %s)", q.ID, q.Type)))
			}
			if q.Validate.Regex != "" {
				diags = append(diags, v.makeError(
					fmt.Sprintf("question %q: regex validation is only meaningful for type: input (found type: %s)", q.ID, q.Type)))
			}
		}
	}
	return diags
}

// validatePresetKnownValidator checks that preset values are recognized.
type validatePresetKnownValidator struct {
	cfgValidator
}

func (v *validatePresetKnownValidator) Run(ctx validate.Context) []validate.Diagnostic {
	validPresets := map[string]bool{
		setup.PresetPort:     true,
		setup.PresetHostname: true,
		setup.PresetPath:     true,
		setup.PresetNonEmpty: true,
	}
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Validate != nil && q.Validate.Preset != "" && !validPresets[q.Validate.Preset] {
			diags = append(diags, v.makeErrorWithHint(
				fmt.Sprintf("question %q: unknown preset %q (supported: port, hostname, path, non-empty)", q.ID, q.Validate.Preset),
				"supported: port, hostname, path, non-empty"))
		}
	}
	return diags
}

// validateRegexCompilesValidator checks that regex patterns compile.
type validateRegexCompilesValidator struct {
	cfgValidator
}

func (v *validateRegexCompilesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Validate != nil && q.Validate.Regex != "" {
			if _, err := regexp.Compile(q.Validate.Regex); err != nil {
				diags = append(diags, v.makeError(
					fmt.Sprintf("question %q: regex pattern fails to compile: %v", q.ID, err)))
			}
		}
	}
	return diags
}

// typeWritesConsistentValidator checks that question type produces compatible values for the writes target.
type typeWritesConsistentValidator struct {
	cfgValidator
}

func (v *typeWritesConsistentValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
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
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %q writes to services.*.enabled which requires type: confirm (found type: %s)", q.ID, q.Type)))
		}
	case "ports":
		if q.Type != setup.TypeInput || q.Validate == nil || q.Validate.Preset != setup.PresetPort {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %q writes to services.*.ports.* which requires type: input with validate.preset: port (found type: %s)", q.ID, q.Type)))
		}
	case "hosts":
		if q.Type != setup.TypeInput {
			diags = append(diags, v.makeError(
				fmt.Sprintf("question %q writes to services.*.hosts.* which requires type: input (found type: %s)", q.ID, q.Type)))
		}
	}
	return diags
}

// requiredConsistentValidator checks that required is consistent with question type.
type requiredConsistentValidator struct {
	cfgValidator
}

func (v *requiredConsistentValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, q := range v.questions() {
		if q.Type == setup.TypeConfirm && q.Required {
			diags = append(diags, v.makeWarning(
				fmt.Sprintf("question %q: type: confirm ignores required (confirm always has a value: true or false)", q.ID)))
		}
	}
	return diags
}
