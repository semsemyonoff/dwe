package ui

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"charm.land/bubbles/v2/key"
	huh "charm.land/huh/v2"
)

// ParamFieldType describes the value shape of a parameter form field.
type ParamFieldType int

// Field types for ParamField.Type.
const (
	FieldTypeUnknown ParamFieldType = iota
	FieldTypeString
	FieldTypePath
	FieldTypeInt
	FieldTypeBool
)

// ParamField is the UI-layer description of one parameter form field. The
// orchestrator (internal/command) translates model.ParamDef → ParamField so
// internal/ui stays free of usercommands/model/config/tpl imports.
//
// IsDefault is set by the orchestrator when the prefilled value came from a
// declared Default / DefaultFrom (i.e. neither --set nor user input). The
// form decorates such fields with a "(default)" title suffix and a
// "default: <value>" description line so users can tell at a glance which
// values they would accept by hitting Enter.
type ParamField struct {
	Name        string
	Type        ParamFieldType
	Description string
	Default     string // raw string prefill (already merged: --set ∪ DefaultFrom ∪ Default)
	IsDefault   bool   // prefilled value originates from Default / DefaultFrom
	Required    bool
	Pattern     string // empty = no pattern check
}

// errRequired is the validation error used for empty required fields.
var errRequired = errors.New("required")

// runFormFn runs the assembled form; swappable in tests. Subtests overriding
// it MUST NOT call t.Parallel() (global state across goroutines).
var runFormFn = func(form *huh.Form) error { return form.Run() }

// paramFormBinding pairs a field name with the *string huh writes into.
type paramFormBinding struct {
	name string
	ptr  *string
}

// BuildParamForm constructs a *huh.Form for the given fields and returns the
// per-field bindings the caller should read from after Run.
//
// Pattern compile errors are surfaced eagerly (NOT panics) so a malformed
// user-authored pattern cannot crash the CLI; resolve.Params validates the
// same patterns lazily at run time.
func BuildParamForm(title string, fields []ParamField) (*huh.Form, []paramFormBinding, error) {
	bindings := make([]paramFormBinding, 0, len(fields))
	huhFields := make([]huh.Field, 0, len(fields))
	for _, f := range fields {
		var re *regexp.Regexp
		if f.Pattern != "" && (f.Type == FieldTypePath || f.Type == FieldTypeString || f.Type == FieldTypeUnknown) {
			compiled, err := regexp.Compile(f.Pattern)
			if err != nil {
				return nil, nil, fmt.Errorf("param %q: invalid pattern %q: %w", f.Name, f.Pattern, err)
			}
			re = compiled
		}
		val := f.Default
		ptr := &val
		field, err := buildField(f, re, ptr)
		if err != nil {
			return nil, nil, err
		}
		huhFields = append(huhFields, field)
		bindings = append(bindings, paramFormBinding{name: f.Name, ptr: ptr})
	}

	keymap := huh.NewDefaultKeyMap()
	keymap.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "quit"))

	form := huh.NewForm(huh.NewGroup(huhFields...).Title(title)).
		WithTheme(Theme()).
		WithKeyMap(keymap).
		WithShowHelp(true)
	return form, bindings, nil
}

func buildField(f ParamField, re *regexp.Regexp, ptr *string) (huh.Field, error) {
	desc := displayDescription(f)
	switch f.Type {
	case FieldTypeBool:
		// huh.Select writes the first option into the bound value on render,
		// so "false" goes first to make it the safe default when the field
		// has no Default/--set prefill.
		// Normalize all ParseBool-accepted forms (e.g. "1", "T") to canonical
		// "true"/"false" so the select highlights the right option.
		// Invalid non-empty values error immediately — mirrors coerceParam in resolve.go.
		if *ptr != "" {
			b, err := strconv.ParseBool(*ptr)
			if err != nil {
				return nil, fmt.Errorf("param %q: cannot parse %q as bool", f.Name, *ptr)
			}
			if b {
				*ptr = "true"
			} else {
				*ptr = "false"
			}
		} else {
			*ptr = "false"
		}
		return huh.NewSelect[string]().
			Title(displayTitle(f)).
			Description(desc).
			Options(huh.NewOption("false", "false"), huh.NewOption("true", "true")).
			Value(ptr), nil

	case FieldTypeInt:
		// Pattern validation is intentionally skipped for int fields: resolve.go:54
		// only enforces patterns on string/path params. Passing nil keeps interactive
		// and non-interactive validation aligned.
		return huh.NewInput().
			Title(displayTitle(f)).
			Description(desc).
			Value(ptr).
			Validate(combineValidators(f, nil, validateInt)), nil

	case FieldTypePath, FieldTypeString, FieldTypeUnknown:
		return huh.NewInput().
			Title(displayTitle(f)).
			Description(desc).
			Value(ptr).
			Validate(combineValidators(f, re, nil)), nil

	default:
		return nil, fmt.Errorf("param %q: unsupported field type %d", f.Name, f.Type)
	}
}

func displayTitle(f ParamField) string {
	title := f.Name
	if f.Required {
		title += " *"
	}
	if f.IsDefault {
		title += " (default)"
	}
	return title
}

// displayDescription augments the user-authored description with a
// "default: <value>" line when the field's prefilled value came from a
// declared default. The augmentation is appended on a new line so an empty
// description stays empty when IsDefault is false.
func displayDescription(f ParamField) string {
	if !f.IsDefault {
		return f.Description
	}
	defaultLine := "default: " + f.Default
	if f.Description == "" {
		return defaultLine
	}
	return f.Description + "\n" + defaultLine
}

func combineValidators(f ParamField, re *regexp.Regexp, extra func(string) error) func(string) error {
	return func(s string) error {
		if f.Required && s == "" {
			return errRequired
		}
		if s == "" {
			return nil
		}
		if re != nil {
			loc := re.FindStringIndex(s)
			if loc == nil || loc[0] != 0 || loc[1] != len(s) {
				return fmt.Errorf("does not match pattern %q", f.Pattern)
			}
		}
		if extra != nil {
			return extra(s)
		}
		return nil
	}
}

func validateInt(s string) error {
	if _, err := strconv.Atoi(s); err != nil {
		return fmt.Errorf("must be an integer")
	}
	return nil
}

// RunParamForm builds the form and runs it. Returns the values map keyed by
// field name. ErrCancelled is returned when the user presses Esc or Ctrl-C.
func RunParamForm(title string, fields []ParamField) (map[string]string, error) {
	form, bindings, err := BuildParamForm(title, fields)
	if err != nil {
		return nil, err
	}
	before, after := snapshotHuhHooks()
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	if err := runFormFn(form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, ErrCancelled
		}
		return nil, err
	}
	values := make(map[string]string, len(bindings))
	for _, b := range bindings {
		values[b.name] = *b.ptr
	}
	return values, nil
}
