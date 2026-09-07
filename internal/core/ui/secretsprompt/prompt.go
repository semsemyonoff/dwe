// Package secretsprompt holds the two interactive halves of the age-identity
// onboarding gate: the hidden form that reads a private identity, and the
// confirmation that offers to open it.
//
// It lives under core/ui because it drives huh forms, and the gate itself
// (core/workflow/keygate) must stay free of core/ui imports; the cli layer
// wraps these two functions into the keygate.PromptFunc / keygate.ConfirmFunc
// closures over the cobra streams. Only cli/* may import this package.
//
// No error produced here carries the typed text or an age parser error over it:
// age's own messages echo input characters, which here are private-key bytes.
package secretsprompt

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// formTitle is shared by both forms so the offer and the input read as one
// step of the same command.
const formTitle = "dwe secrets › key import"

// runAsk is the package-level test seam over ask.Run (mirroring the seam in
// internal/cli/secrets/set.go). Tests stub it to drive the forms
// deterministically; they MUST NOT call t.Parallel() while overriding it.
var runAsk = ask.Run

// quitSpec binds Esc alongside Ctrl-C on both forms. huh's default keymap quits
// on Ctrl-C only, so without this the documented Esc cancel does nothing and a
// developer who opened the offer from `dwe run` has no way out of a hidden
// field. Declared once: the two forms are one step and must cancel alike.
func quitSpec() *ask.QuitSpec {
	return &ask.QuitSpec{Keys: []string{"esc", "ctrl+c"}, Help: "cancel"}
}

// PromptIdentity reads the private identity for recipient from a single hidden
// field. Validation runs in-form, so a typo or a foreign key is corrected
// without losing the prompt; Esc cancels and surfaces as widgets.ErrCancelled.
func PromptIdentity(ctx context.Context, recipient string, in io.Reader, out io.Writer) (secrets.Identity, error) {
	fields := []ask.Field{{
		Key:         "identity",
		Title:       "Private identity for " + recipient,
		Description: "Paste the AGE-SECRET-KEY-… line, or the whole keyfile; the typed characters are not echoed.",
		Kind:        ask.FieldPassword,
		Required:    true,
		Validate:    identityValidator(recipient),
	}}
	res, err := runAsk(ctx, formTitle, fields, ask.RunOptions{Input: in, Output: out, Quit: quitSpec()})
	if err != nil {
		return secrets.Identity{}, err
	}
	// Re-checked rather than trusted: a stubbed runAsk (and huh's own
	// accessible mode) can hand back a value the field validator never saw.
	id, err := secrets.ParseIdentity(res.String("identity"))
	if err != nil {
		return secrets.Identity{}, errors.New(parseMessage)
	}
	if id.Recipient() != recipient {
		return secrets.Identity{}, errors.New(mismatchMessage(id.Recipient(), recipient))
	}
	return id, nil
}

// ConfirmImport asks whether to enter the identity now. The buttons are labelled
// after the two outcomes rather than yes/no: the decline aborts the command.
func ConfirmImport(ctx context.Context, explanation string, in io.Reader, out io.Writer) (bool, error) {
	fields := []ask.Field{{
		Key:         "import",
		Title:       "Enter the private identity now?",
		Description: explanation,
		Kind:        ask.FieldConfirm,
		Affirmative: "Enter key",
		Negative:    "Abort",
	}}
	res, err := runAsk(ctx, formTitle, fields, ask.RunOptions{Input: in, Output: out, Quit: quitSpec()})
	if err != nil {
		return false, err
	}
	return res.Bool("import"), nil
}

// parseMessage is the fixed wording for "this is not an age identity". The age
// parser's own error is never shown: it interpolates the offending characters.
const parseMessage = "not an age identity — paste the AGE-SECRET-KEY-… line printed by 'dwe secrets key export'"

// mismatchMessage names both public recipients, which is the whole diagnosis:
// the key is valid, it just belongs to another project.
func mismatchMessage(got, want string) string {
	return fmt.Sprintf("this identity is for %s, but the project uses %s", got, want)
}

// identityValidator is the in-form check: parse, then match the recipient.
// Exercised directly by the tests — it is the surface that sees the raw typed
// text, so its messages are the ones that must never quote it.
func identityValidator(recipient string) func(string) error {
	return func(text string) error {
		id, err := secrets.ParseIdentity(text)
		if err != nil {
			return errors.New(parseMessage)
		}
		if id.Recipient() != recipient {
			return errors.New(mismatchMessage(id.Recipient(), recipient))
		}
		return nil
	}
}
