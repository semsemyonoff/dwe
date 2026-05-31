package ask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/semsemyonoff/devbox/internal/core/ui/styles"
	"github.com/semsemyonoff/devbox/internal/core/ui/widgets"

	huh "charm.land/huh/v2"
)

// FieldKind identifies a form field type.
type FieldKind int

const (
	// FieldUnknown is the zero value; Run rejects any Field with this kind.
	FieldUnknown FieldKind = iota
	// FieldInput is a free-text input field.
	FieldInput
	// FieldSelect is a single-choice select field.
	FieldSelect
	// FieldMultiselect is a multi-choice select field.
	FieldMultiselect
	// FieldConfirm is a yes/no confirmation field.
	FieldConfirm
)

// Option represents a single choice in a select or multiselect field.
type Option struct {
	Value string
	Label string
}

// Field describes one form field.
type Field struct {
	Key         string             // field key, used in Result
	Title       string             // prompt title
	Description string             // additional help text
	Kind        FieldKind          // field type (input/select/multiselect/confirm)
	Required    bool               // if true, huh validates non-empty on submit
	Default     string             // prefilled value (scalar); for multiselect this is the joined default
	Defaults    []string           // pre-selected values (multiselect only)
	Options     []Option           // choices for select/multiselect
	Validate    func(string) error // optional per-field validation; for multiselect, called per-item
}

// Result is the form output. Values are typed: string for input/select,
// []string for multiselect, bool for confirm. Use the typed accessors
// instead of directly accessing the underlying map.
type Result struct {
	values map[string]any
}

// String returns the string value for key, or "" if missing or wrong type.
func (r Result) String(key string) string {
	v, ok := r.values[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// Strings returns the []string value for key, or nil if missing or wrong type.
func (r Result) Strings(key string) []string {
	v, ok := r.values[key]
	if !ok {
		return nil
	}
	ss, ok := v.([]string)
	if !ok {
		return nil
	}
	return ss
}

// Bool returns the bool value for key, or false if missing or wrong type.
func (r Result) Bool(key string) bool {
	v, ok := r.values[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		return false
	}
	return b
}

// NewResultForTest creates a Result with the given map for testing purposes.
func NewResultForTest(values map[string]any) Result {
	return Result{values: values}
}

// Has reports whether the Result contains a value for key (i.e., the field was present in the form).
func (r Result) Has(key string) bool {
	_, ok := r.values[key]
	return ok
}

// IsEmpty reports whether the Result has no values (for testing).
func (r Result) IsEmpty() bool {
	return len(r.values) == 0
}

// RunOptions controls how Run executes.
type RunOptions struct {
	Input  io.Reader // defaults to os.Stdin if zero
	Output io.Writer // defaults to os.Stdout if zero
}

// Run displays a form with the given fields and blocks until the user
// submits or cancels. Uses huh.Form.RunWithContext so context cancellation
// (Ctrl-C, parent timeout) aborts the form cleanly. Returns huh.ErrUserAborted
// if the user cancels; the caller is responsible for mapping that to a
// user-facing error like widgets.ErrCancelled.
func Run(ctx context.Context, title string, fields []Field, opts RunOptions) (Result, error) {
	if opts.Input == nil {
		opts.Input = os.Stdin
	}
	if opts.Output == nil {
		opts.Output = os.Stdout
	}

	// Validate that no field has FieldUnknown.
	for _, f := range fields {
		if f.Kind == FieldUnknown {
			return Result{}, fmt.Errorf("field %q: kind is FieldUnknown (zero value)", f.Key)
		}
	}

	// Handle empty field list early.
	if len(fields) == 0 {
		return Result{values: make(map[string]any)}, nil
	}

	// Build huh fields and collect bindings.
	// bindings holds the pointers that huh will update during form.Run.
	huhFields := make([]huh.Field, 0, len(fields))
	bindings := make([]fieldBinding, 0, len(fields))

	for _, f := range fields {
		huhField, binding, err := buildHuhField(f)
		if err != nil {
			return Result{}, err
		}
		huhFields = append(huhFields, huhField)
		bindings = append(bindings, binding)
	}

	// Build and run the form, wrapped in the canonical prompt hooks.
	form := huh.NewForm(
		huh.NewGroup(huhFields...).Title(title),
	).
		WithTheme(styles.Theme()).
		WithInput(opts.Input).
		WithOutput(opts.Output)

	err := widgets.RunWithPromptHooks(func() error {
		return form.RunWithContext(ctx)
	})
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return Result{}, huh.ErrUserAborted
		}
		return Result{}, err
	}

	// Extract final values from bindings (huh updated them in-place).
	result := make(map[string]any)
	for _, binding := range bindings {
		switch v := binding.ptr.(type) {
		case *string:
			result[binding.key] = *v
		case *[]string:
			result[binding.key] = *v
		case *bool:
			result[binding.key] = *v
		}
	}

	return Result{values: result}, nil
}

// fieldBinding holds metadata about a bound huh field and the pointer
// that huh will update during form.Run.
type fieldBinding struct {
	key string
	ptr any // *string, *[]string, or *bool
}

// buildHuhField constructs a single huh.Field for the given Field,
// returning the field and binding info.
func buildHuhField(f Field) (huh.Field, fieldBinding, error) {
	switch f.Kind {
	case FieldInput:
		val := f.Default
		field := huh.NewInput().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Value(&val)

		if f.Required {
			field = field.Validate(func(s string) error {
				if s == "" {
					return errors.New("required")
				}
				if f.Validate != nil {
					return f.Validate(s)
				}
				return nil
			})
		} else if f.Validate != nil {
			field = field.Validate(f.Validate)
		}

		return field, fieldBinding{key: f.Key, ptr: &val}, nil

	case FieldSelect:
		val := f.Default
		opts := make([]huh.Option[string], len(f.Options))
		for i, opt := range f.Options {
			opts[i] = huh.NewOption(opt.Label, opt.Value)
		}

		field := huh.NewSelect[string]().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Options(opts...).
			Value(&val)

		if f.Required {
			field = field.Validate(func(s string) error {
				if s == "" {
					return errors.New("required")
				}
				if f.Validate != nil {
					return f.Validate(s)
				}
				return nil
			})
		} else if f.Validate != nil {
			field = field.Validate(f.Validate)
		}

		return field, fieldBinding{key: f.Key, ptr: &val}, nil

	case FieldMultiselect:
		val := f.Defaults
		if val == nil {
			val = []string{}
		}
		opts := make([]huh.Option[string], len(f.Options))
		for i, opt := range f.Options {
			opts[i] = huh.NewOption(opt.Label, opt.Value)
		}

		field := huh.NewMultiSelect[string]().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Options(opts...).
			Value(&val)

		if f.Required || f.Validate != nil {
			// Wrap the validate func to check per-item for multiselect
			// (though "required" for multiselect means "at least one item").
			field = field.Validate(func(selected []string) error {
				if f.Required && len(selected) == 0 {
					return errors.New("required")
				}
				if f.Validate != nil {
					// Validate each selected item.
					for _, item := range selected {
						if err := f.Validate(item); err != nil {
							return err
						}
					}
				}
				return nil
			})
		}

		return field, fieldBinding{key: f.Key, ptr: &val}, nil

	case FieldConfirm:
		val := false
		// Parse f.Default as a bool if provided.
		if f.Default != "" {
			// Try to parse as bool; if it fails, default to false.
			switch f.Default {
			case "true", "1", "yes", "y":
				val = true
			case "false", "0", "no", "n":
				val = false
			}
		}

		field := huh.NewConfirm().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Value(&val)

		if f.Validate != nil {
			field = field.Validate(func(b bool) error {
				// Convert bool to string for validation callback.
				return f.Validate(fmt.Sprintf("%v", b))
			})
		}

		return field, fieldBinding{key: f.Key, ptr: &val}, nil

	default:
		return nil, fieldBinding{}, fmt.Errorf("field %q: unsupported kind %d", f.Key, f.Kind)
	}
}
