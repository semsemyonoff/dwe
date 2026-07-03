package ask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"

	"charm.land/bubbles/v2/key"
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
	Height      int                // select/multiselect viewport height; 0 = unset (huh default)
	Filterable  *bool              // FieldMultiselect only (huh/v2 Select has no Filterable); nil = huh default
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

// QuitSpec declaratively overrides the form's quit keybinding. Every form
// site that needs a custom quit binding (esc cancel / q exit / back, etc.)
// configures one instead of hand-rolling a raw huh.NewForm + keymap.
type QuitSpec struct {
	Keys []string // e.g. []string{"q", "esc", "ctrl+c"}
	Help string   // help-line verb: "cancel" / "back" / "exit"
}

// RunOptions controls how Run executes.
type RunOptions struct {
	Input      io.Reader // defaults to os.Stdin if zero
	Output     io.Writer // defaults to os.Stdout if zero
	Quit       *QuitSpec // nil = huh defaults (no keymap customization); empty Keys treated as nil
	SubmitHelp string    // cosmetic: relabel the submit help verb ("select"); "" = huh default
	ShowHelp   *bool     // nil = leave huh default (shown); non-nil = WithShowHelp(*v)
}

// Form is a form built by Build: runnable via Run, or introspectable via
// Huh()/Result() so Stage 7 can embed it as a capturing-overlay child model
// (driving Update/View directly instead of calling Run).
type Form struct {
	huh      *huh.Form
	bindings []fieldBinding
	empty    bool // true when built from zero fields; Run short-circuits without invoking huh
}

// Build constructs a Form from fields and opts without running it. Mirrors
// the validation and construction Run used to do inline; Run is now
// Build + (*Form).Run.
func Build(title string, fields []Field, opts RunOptions) (*Form, error) {
	if opts.Input == nil {
		opts.Input = os.Stdin
	}
	if opts.Output == nil {
		opts.Output = os.Stdout
	}

	// Validate that no field has FieldUnknown.
	for _, f := range fields {
		if f.Kind == FieldUnknown {
			return nil, fmt.Errorf("field %q: kind is FieldUnknown (zero value)", f.Key)
		}
	}

	// Handle empty field list early.
	if len(fields) == 0 {
		return &Form{empty: true}, nil
	}

	// Build huh fields and collect bindings.
	// bindings holds the pointers that huh will update during form.Run.
	huhFields := make([]huh.Field, 0, len(fields))
	bindings := make([]fieldBinding, 0, len(fields))

	hasQuit := opts.Quit != nil && len(opts.Quit.Keys) > 0
	for _, f := range fields {
		huhField, binding, err := buildHuhField(f, hasQuit)
		if err != nil {
			return nil, err
		}
		huhFields = append(huhFields, huhField)
		bindings = append(bindings, binding)
	}

	form := huh.NewForm(
		huh.NewGroup(huhFields...).Title(title),
	).
		WithTheme(styles.Theme()).
		WithInput(opts.Input).
		WithOutput(opts.Output).
		WithKeyMap(buildKeyMap(opts, fields))

	if opts.ShowHelp != nil {
		form = form.WithShowHelp(*opts.ShowHelp)
	}

	return &Form{huh: form, bindings: bindings}, nil
}

// Run executes the form via widgets.RunHuhForm (prompt hooks + context-aware
// run + abort translation) and harvests the result. Returns
// widgets.ErrCancelled if the user cancels.
func (f *Form) Run(ctx context.Context) (Result, error) {
	if f.empty {
		return Result{values: make(map[string]any)}, nil
	}
	if err := widgets.RunHuhForm(ctx, f.huh); err != nil {
		return Result{}, err
	}
	return f.Result(), nil
}

// Huh exposes the underlying huh.Form. Stage 7 embeds this as a
// capturing-overlay child model instead of calling Run.
func (f *Form) Huh() *huh.Form {
	return f.huh
}

// Result harvests the current bound values from the form's fields. Call
// after the form completes, whether via Run or externally when the form is
// driven as a child model.
func (f *Form) Result() Result {
	result := make(map[string]any)
	for _, binding := range f.bindings {
		switch v := binding.ptr.(type) {
		case *string:
			result[binding.key] = *v
		case *[]string:
			result[binding.key] = *v
		case *bool:
			result[binding.key] = *v
		}
	}
	return Result{values: result}
}

// Run displays a form with the given fields and blocks until the user
// submits or cancels. Uses huh.Form.RunWithContext so context cancellation
// (Ctrl-C, parent timeout) aborts the form cleanly. Returns
// widgets.ErrCancelled if the user cancels. Run is Build + (*Form).Run.
func Run(ctx context.Context, title string, fields []Field, opts RunOptions) (Result, error) {
	form, err := Build(title, fields, opts)
	if err != nil {
		return Result{}, err
	}
	return form.Run(ctx)
}

// fieldBinding holds metadata about a bound huh field and the pointer
// that huh will update during form.Run.
type fieldBinding struct {
	key string
	ptr any // *string, *[]string, or *bool
}

// presentKinds records which field kinds appear in a form, driving which
// keymap slots buildKeyMap touches (a form-wide keymap only makes sense to
// hijack for the kinds actually present).
type presentKinds struct {
	input, selectKind, multiselect bool
}

func detectKinds(fields []Field) presentKinds {
	var p presentKinds
	for _, f := range fields {
		switch f.Kind {
		case FieldInput:
			p.input = true
		case FieldSelect:
			p.selectKind = true
		case FieldMultiselect:
			p.multiselect = true
		}
	}
	return p
}

// buildKeyMap assembles the form-wide huh.KeyMap from RunOptions: a
// declarative Quit override and a cosmetic SubmitHelp relabel. Both are
// no-ops on huh's own default keymap when unset, so this always returns a
// safe keymap to pass to huh.Form.WithKeyMap (huh.NewForm already applies
// huh.NewDefaultKeyMap() internally, so re-applying an equivalent default
// here changes nothing for callers that set neither option).
//
// huh hides the form-level Quit binding from field help, so the quit hint
// is surfaced by hijacking another binding's help slot per field kind
// present: select/multiselect → their Filter slot (always visible for
// select; visible for multiselect only when Filterable isn't false — see
// Field.Filterable), input → AcceptSuggestion (paired with a fake
// SuggestionsFunc in buildHuhField so huh exposes that binding at all). The
// hijacked binding's own key press is caught by the form-level Quit handler
// first, so it never actually fires — only its help label renders.
func buildKeyMap(opts RunOptions, fields []Field) *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	kinds := detectKinds(fields)

	if opts.SubmitHelp != "" {
		if kinds.selectKind {
			km.Select.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", opts.SubmitHelp))
		}
		if kinds.multiselect {
			km.MultiSelect.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", opts.SubmitHelp))
		}
		if kinds.input {
			km.Input.Next = key.NewBinding(key.WithKeys("enter", "tab"), key.WithHelp("enter", opts.SubmitHelp))
		}
	}

	if opts.Quit != nil && len(opts.Quit.Keys) > 0 {
		joined := strings.Join(opts.Quit.Keys, "/")
		binding := key.NewBinding(key.WithKeys(opts.Quit.Keys...), key.WithHelp(joined, opts.Quit.Help))
		km.Quit = binding
		if kinds.selectKind {
			km.Select.Filter = binding
		}
		if kinds.multiselect {
			km.MultiSelect.Filter = binding
		}
		if kinds.input {
			km.Input.AcceptSuggestion = binding
		}
	}

	return km
}

// buildHuhField constructs a single huh.Field for the given Field,
// returning the field and binding info. hasQuit indicates the form has a
// QuitSpec in effect: input fields need a fake SuggestionsFunc so huh
// exposes the AcceptSuggestion binding that carries the quit hint (see
// buildKeyMap).
func buildHuhField(f Field, hasQuit bool) (huh.Field, fieldBinding, error) {
	if f.Filterable != nil && f.Kind != FieldMultiselect {
		return nil, fieldBinding{}, fmt.Errorf("field %q: Filterable is only valid for FieldMultiselect", f.Key)
	}

	switch f.Kind {
	case FieldInput:
		val := f.Default
		field := huh.NewInput().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Value(&val)

		if hasQuit {
			// Enable suggestions so huh.Input.KeyBinds() exposes the
			// AcceptSuggestion binding, which buildKeyMap hijacks to show
			// the quit hint in the help line. No real suggestions are
			// presented (the func returns a single blank entry).
			field = field.SuggestionsFunc(func() []string { return []string{" "} }, nil)
		}

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

		if f.Height > 0 {
			field = field.Height(f.Height)
		}

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

		if f.Height > 0 {
			field = field.Height(f.Height)
		}
		if f.Filterable != nil {
			field = field.Filterable(*f.Filterable)
		}

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
