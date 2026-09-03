package secretsprompt

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// stubAsk replaces the form executor for one test. Package-level state:
// callers MUST NOT run in parallel.
func stubAsk(t *testing.T, fn func(ctx context.Context, title string, fields []ask.Field, opts ask.RunOptions) (ask.Result, error)) {
	t.Helper()
	prev := runAsk
	runAsk = fn
	t.Cleanup(func() { runAsk = prev })
}

func keygen(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return id
}

// keyTokenRe matches real key material. The bare "AGE-SECRET-KEY-…" prefix is
// deliberately NOT the signal: it appears in the instructional wording, so the
// assertion looks for a prefix followed by actual bech32 payload.
var keyTokenRe = regexp.MustCompile(`AGE-SECRET-KEY-1[A-Z0-9]{6,}`)

// assertNoKeyLeak pins that a message carries neither key material nor the
// tail of the typed text: a prefix-only check would pass on a truncated echo.
func assertNoKeyLeak(t *testing.T, typed, message string) {
	t.Helper()
	if keyTokenRe.MatchString(message) {
		t.Errorf("message leaked private key material: %q", message)
	}
	if len(typed) >= 20 && strings.Contains(message, typed[len(typed)-20:]) {
		t.Errorf("message leaked the tail of the typed input: %q", message)
	}
}

// TestPromptIdentity_ReturnsMatchingIdentity is the happy path: the typed text
// parses and belongs to the project.
func TestPromptIdentity_ReturnsMatchingIdentity(t *testing.T) {
	id := keygen(t)
	var gotTitle string
	var gotFields []ask.Field
	stubAsk(t, func(_ context.Context, title string, fields []ask.Field, _ ask.RunOptions) (ask.Result, error) {
		gotTitle, gotFields = title, fields
		return ask.NewResultForTest(map[string]any{"identity": id.Export()}), nil
	})

	got, err := PromptIdentity(t.Context(), id.Recipient(), nil, nil)
	if err != nil {
		t.Fatalf("PromptIdentity: %v", err)
	}
	if got.Recipient() != id.Recipient() {
		t.Errorf("recipient = %q, want %q", got.Recipient(), id.Recipient())
	}
	if gotTitle != formTitle {
		t.Errorf("title = %q, want %q", gotTitle, formTitle)
	}
	if len(gotFields) != 1 || gotFields[0].Kind != ask.FieldPassword {
		t.Fatalf("fields = %+v, want one hidden field", gotFields)
	}
	if !strings.Contains(gotFields[0].Title, id.Recipient()) {
		t.Errorf("field title %q does not name the recipient", gotFields[0].Title)
	}
	if gotFields[0].Validate == nil {
		t.Error("the field carries no in-form validation")
	}
}

// TestPromptIdentity_RevalidatesResult pins that the returned value is checked
// even when the field validator never ran (a stubbed executor, huh's accessible
// mode), and that neither refusal quotes the typed text.
func TestPromptIdentity_RevalidatesResult(t *testing.T) {
	project := keygen(t)
	foreign := keygen(t)

	tests := []struct {
		name  string
		typed string
		want  string
	}{
		{name: "garbage", typed: "definitely not a key at all, really not", want: "not an age identity"},
		{name: "foreign key", typed: foreign.Export(), want: foreign.Recipient()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubAsk(t, func(context.Context, string, []ask.Field, ask.RunOptions) (ask.Result, error) {
				return ask.NewResultForTest(map[string]any{"identity": tt.typed}), nil
			})
			_, err := PromptIdentity(t.Context(), project.Recipient(), nil, nil)
			if err == nil {
				t.Fatal("PromptIdentity accepted a value it must refuse")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
			assertNoKeyLeak(t, tt.typed, err.Error())
		})
	}
}

// TestPromptIdentity_CancelPropagates pins that Esc reaches the caller as
// widgets.ErrCancelled rather than being swallowed into a parse failure.
func TestPromptIdentity_CancelPropagates(t *testing.T) {
	id := keygen(t)
	stubAsk(t, func(context.Context, string, []ask.Field, ask.RunOptions) (ask.Result, error) {
		return ask.Result{}, widgets.ErrCancelled
	})

	if _, err := PromptIdentity(t.Context(), id.Recipient(), nil, nil); !errors.Is(err, widgets.ErrCancelled) {
		t.Fatalf("err = %v, want widgets.ErrCancelled", err)
	}
}

// TestIdentityValidator is the in-form check exercised directly: it is the one
// surface that sees the raw typed text, so its messages are the ones that must
// never quote it.
func TestIdentityValidator(t *testing.T) {
	project := keygen(t)
	foreign := keygen(t)
	validate := identityValidator(project.Recipient())

	if err := validate(project.Export()); err != nil {
		t.Errorf("the project's own identity was refused: %v", err)
	}
	// A whole keyfile pasted into the single-line field arrives with its comment
	// joined onto the key; ParseIdentity is what makes that shape work here.
	if err := validate("# public key: " + project.Recipient() + " " + project.Export()); err != nil {
		t.Errorf("a pasted keyfile was refused: %v", err)
	}

	err := validate(foreign.Export())
	if err == nil {
		t.Fatal("a foreign identity was accepted")
	}
	// Both public recipients are the diagnosis: the key is valid, it belongs
	// elsewhere.
	if !strings.Contains(err.Error(), foreign.Recipient()) || !strings.Contains(err.Error(), project.Recipient()) {
		t.Errorf("mismatch error %q must name both recipients", err)
	}
	assertNoKeyLeak(t, foreign.Export(), err.Error())

	const garbage = "AGE-SECRET-KEY-1-this-is-not-really-a-key-0123456789"
	err = validate(garbage)
	if err == nil {
		t.Fatal("garbage was accepted")
	}
	if !strings.Contains(err.Error(), "not an age identity") {
		t.Errorf("parse error = %q, want the fixed DWE wording", err)
	}
	assertNoKeyLeak(t, garbage, err.Error())
}

// TestConfirmImport pins the offer: the explanation reaches the form, the
// caller's streams are honoured, and the buttons are labelled after the two
// outcomes rather than yes/no.
func TestConfirmImport(t *testing.T) {
	in := strings.NewReader("")
	out := &strings.Builder{}

	var gotFields []ask.Field
	var gotOpts ask.RunOptions
	stubAsk(t, func(_ context.Context, _ string, fields []ask.Field, opts ask.RunOptions) (ask.Result, error) {
		gotFields, gotOpts = fields, opts
		return ask.NewResultForTest(map[string]any{"import": true}), nil
	})

	ok, err := ConfirmImport(t.Context(), "because reasons", in, out)
	if err != nil || !ok {
		t.Fatalf("ConfirmImport = (%v, %v), want (true, nil)", ok, err)
	}
	if len(gotFields) != 1 || gotFields[0].Kind != ask.FieldConfirm {
		t.Fatalf("fields = %+v, want one confirm field", gotFields)
	}
	if gotFields[0].Description != "because reasons" {
		t.Errorf("description = %q, want the explanation", gotFields[0].Description)
	}
	if gotFields[0].Affirmative != "Enter key" || gotFields[0].Negative != "Abort" {
		t.Errorf("buttons = %q/%q, want Enter key/Abort", gotFields[0].Affirmative, gotFields[0].Negative)
	}
	if gotOpts.Input != in || gotOpts.Output != out {
		t.Error("ConfirmImport did not pass the caller's streams through")
	}
}

// TestConfirmImport_Declined pins that a decline is data, not an error.
func TestConfirmImport_Declined(t *testing.T) {
	stubAsk(t, func(context.Context, string, []ask.Field, ask.RunOptions) (ask.Result, error) {
		return ask.NewResultForTest(map[string]any{"import": false}), nil
	})
	ok, err := ConfirmImport(t.Context(), "why", nil, nil)
	if err != nil || ok {
		t.Fatalf("ConfirmImport = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestConfirmImport_CancelPropagates pins Esc on the offer.
func TestConfirmImport_CancelPropagates(t *testing.T) {
	stubAsk(t, func(context.Context, string, []ask.Field, ask.RunOptions) (ask.Result, error) {
		return ask.Result{}, widgets.ErrCancelled
	})
	if _, err := ConfirmImport(t.Context(), "why", nil, nil); !errors.Is(err, widgets.ErrCancelled) {
		t.Fatalf("err = %v, want widgets.ErrCancelled", err)
	}
}
