// Package keygate is the shared "this project needs an age identity you do not
// have — want to enter it now?" gate, plus the inventory scanner every
// `dwe secrets` surface reports from.
//
// It runs on the RAW config layers, before LoadConfig, so a caller loads the
// config exactly once and already decrypted: there is no reload step and no
// window in which a wizard proceeds with still-unresolved state.
//
// The package owns the decision, the scan and the keyfile write. The two
// interactive pieces arrive as function values in Options (Prompt, Confirm),
// implemented in internal/core/ui/secretsprompt and wired by the cli layer —
// that is what keeps this package free of any core/ui import (§ Dependency
// Rules, ui-is-sink). With either function nil the gate behaves exactly as it
// does in a non-interactive context.
//
// Nothing here traces or logs the submitted key text or a parser error over it:
// the gate runs before LoadConfig, i.e. before trace.RegisterRedaction is
// installed, so there is no redactor to fall back on.
package keygate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// PromptFunc reads a private identity from the user. It is implemented by
// secretsprompt.PromptIdentity and injected by the cli layer.
type PromptFunc func(ctx context.Context, recipient string) (secrets.Identity, error)

// ConfirmFunc asks whether to enter the identity now, explanation being the
// count-free sentence Ensure composed. Implemented by
// secretsprompt.ConfirmImport.
type ConfirmFunc func(ctx context.Context, explanation string) (bool, error)

// The three refusals Ensure can return. Every caller maps all three onto its
// own typed envelope, so they are sentinels rather than messages.
var (
	// ErrAborted means the user declined the offer, or cancelled the form. No
	// keyfile was written and nothing else changed.
	ErrAborted = errors.New("identity import aborted")
	// ErrEnvSourceUnusable means DWE_AGE_KEY / DWE_AGE_KEY_FILE is set but does
	// not hold this project's identity. LoadIdentity is first-present-source-
	// wins, so a freshly written keyfile would not even be consulted — the fix
	// is to unset or repair the variable, never to import a key.
	ErrEnvSourceUnusable = errors.New("identity environment source unusable")
	// ErrKeyfileUnusable means the canonical keyfile exists but does not load as
	// this project's identity. WriteKeyfile is O_EXCL, so an import could not
	// replace it; the user has to remove it first.
	ErrKeyfileUnusable = errors.New("keyfile unusable")
)

// Options configures one Ensure call. Every interactivity input is passed in
// rather than probed here: the caller owns the streams, the flags and the
// environment reading, so the gate stays testable without a terminal.
type Options struct {
	BaseDir        string         // project root; "" disables the keyfile-write locks
	Layers         []config.Layer // raw layers (config.LoadRawLayers); nil → gate skipped
	Interactive    bool           // caller-evaluated: widgets.IsInteractiveFn(stdin)
	Yes            bool           // --yes (only run/restart define it)
	OutputJSON     bool           // --output json (root persistent flag)
	NonInteractive bool           // DWE_NONINTERACTIVE: cmdctx.NonInteractiveEnv() or NonInteractiveEnv()
	Prompt         PromptFunc
	Confirm        ConfirmFunc
	Out            io.Writer // the success report; nil → discarded
}

// Ensure offers to import the project's private identity when it is missing and
// something in the project actually needs it.
//
// It returns (false, nil) whenever nothing had to be done — no raw layers, a
// layer set that would not load anyway, no or malformed recipient, no encrypted
// surface, a usable identity already present, or a non-interactive context. In
// every one of those cases the caller's existing failure path is left to speak,
// unchanged; the gate is never the thing that reports a config problem.
//
// It returns (true, nil) after a verified import, and an error only when the
// user declined (ErrAborted), an environment source is present but unusable
// (ErrEnvSourceUnusable), the canonical keyfile is present but unusable
// (ErrKeyfileUnusable), or the import itself failed.
func Ensure(ctx context.Context, opts Options) (imported bool, err error) {
	// LoadRawLayers does not validate, and a misplaced secrets: block must
	// surface as today's config error rather than as a prompt.
	if opts.Layers == nil || config.ValidateLayerRoots(opts.Layers) != nil {
		return false, nil
	}
	recipient := config.RecipientFromLayers(opts.Layers)
	// A malformed recipient is the secrets.recipient validator's story: a prompt
	// whose match can never succeed must not open.
	if recipient == "" || secrets.ParseRecipient(recipient) != nil {
		return false, nil
	}
	if _, _, lerr := secrets.LoadIdentity(recipient); lerr == nil {
		return false, nil
	}
	if !HasEncryptedSurface(opts.BaseDir, opts.Layers) {
		return false, nil
	}
	// The next two refusals fire in EVERY mode, non-interactive included: both
	// are more precise than the caller's own message and neither can be fixed by
	// typing a key.
	if err := envSourceUnusable(recipient); err != nil {
		return false, err
	}
	if err := keyfileUnusable(recipient); err != nil {
		return false, err
	}
	if !opts.Interactive || opts.NonInteractive || opts.Yes || opts.OutputJSON ||
		opts.Prompt == nil || opts.Confirm == nil {
		return false, nil
	}

	// The offer is count-free — HasEncryptedSurface is a bool probe, and counting
	// would mean decrypting before there is a key. Counts appear in the report.
	ok, err := opts.Confirm(ctx, Explanation(recipient))
	if err != nil || !ok {
		return false, aborted(recipient)
	}
	id, err := opts.Prompt(ctx, recipient)
	if err != nil {
		return false, aborted(recipient)
	}
	if err := importIdentity(opts, recipient, id); err != nil {
		return false, err
	}
	return true, nil
}

// Explanation is the sentence the confirmation shows: what is blocked, why a
// key is needed and where it would be looked up.
func Explanation(recipient string) string {
	return fmt.Sprintf("This project has encrypted values that need the age identity for %s, "+
		"and this machine does not have it. %s", recipient, secrets.IdentityHint(recipient))
}

// importIdentity stores the accepted identity and reports what it opened. The
// project locks are held around the write for the same reason `key import`
// holds them: an import racing a `rekey` would install a key the rekey retires.
func importIdentity(opts Options, recipient string, id secrets.Identity) error {
	// A hand-rolled PromptFunc may skip the form's own validation, and a keyfile
	// stored under a foreign recipient's name looks installed and opens nothing.
	if id.IsZero() || id.Recipient() != recipient {
		return fmt.Errorf("the supplied identity is for %s, but the project uses %s", id.Recipient(), recipient)
	}

	release := func() {}
	if opts.BaseDir != "" {
		r, err := lock.AcquireProjectLocks(opts.BaseDir)
		if err != nil {
			return err
		}
		release = r
	}
	path, err := secrets.WriteKeyfile(id)
	release()
	if err != nil {
		return fmt.Errorf("store the identity: %w", err)
	}

	// Verify through the real lookup rather than trusting the write: a keyfile
	// that does not load back is a failure, not a success with a caveat.
	if _, _, err := secrets.LoadIdentity(recipient); err != nil {
		return fmt.Errorf("the identity was stored at %s but does not load back: %w", path, err)
	}

	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	_, _ = fmt.Fprintf(out, "identity for %s stored at %s\n", recipient, path)
	// A scan that could not run is reported, never counted: the key IS stored,
	// so this is not a failure, but silence here reads as "nothing to report"
	// and a zero count would read as "your key opens nothing".
	if inv, err := Inventory(opts.BaseDir, opts.Layers, LoadIdentitySet(recipient)); err == nil {
		markers, files := inv.Readable()
		_, _ = fmt.Fprintf(out, "%d encrypted value(s) and %d .age file(s) are now readable\n", markers, files)
	} else {
		_, _ = fmt.Fprintf(out, "the readability report could not be built: %v\n", err)
	}
	return nil
}

// envSourceUnusable refuses a present-but-broken environment source. The
// variable's VALUE is never echoed: it is private key text.
func envSourceUnusable(recipient string) error {
	name := ""
	switch {
	case os.Getenv(secrets.EnvKey) != "":
		name = secrets.EnvKey
	case os.Getenv(secrets.EnvKeyFile) != "":
		name = secrets.EnvKeyFile
	default:
		return nil
	}
	return fmt.Errorf("%w: $%s is set but does not hold the identity for %s; unset it or fix it — "+
		"a keyfile is not consulted while it is set", ErrEnvSourceUnusable, name, recipient)
}

// keyfileUnusable refuses a canonical keyfile that exists but failed the
// lookup: unreadable, or holding somebody else's key.
//
// Lstat, not Stat: WriteKeyfile is O_EXCL, which fails on a dangling symlink
// just as it does on a regular file, so the path ENTRY is what makes an import
// impossible. Following the link would let the gate open a form whose write is
// already doomed — the developer would hand over a private key for nothing.
func keyfileUnusable(recipient string) error {
	path, err := secrets.KeyfilePath(recipient)
	if err != nil {
		// No home directory, no keyfile path — WriteKeyfile would fail on the
		// same resolution after the form collected a private identity.
		return fmt.Errorf("%w: the keyfile path for %s cannot be resolved (%v)",
			ErrKeyfileUnusable, recipient, err)
	}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		// The path could not be inspected at all — a locked-down keys
		// directory. Prompting here would take a private key and then fail the
		// O_EXCL write on the same inaccessible path, so the gate stops with
		// what to fix instead.
		return fmt.Errorf("%w: %s cannot be inspected (%v); fix the permissions on the keys "+
			"directory and retry", ErrKeyfileUnusable, path, err)
	}
	return fmt.Errorf("%w: %s exists but does not hold a usable identity for %s; "+
		"remove it with 'dwe secrets key remove %s' and import the right one",
		ErrKeyfileUnusable, path, recipient, recipient)
}

// aborted is the refusal after a decline or a cancelled form. The cause is
// deliberately not interpolated: a cancel adds nothing to the sentence, and a
// form failure is the one error whose text could have travelled next to the
// typed key.
func aborted(recipient string) error {
	return fmt.Errorf("%w: no identity was imported; %s", ErrAborted, secrets.IdentityHint(recipient))
}

// NonInteractiveEnv reports whether DWE_NONINTERACTIVE is truthy ("1" or
// "true"). It duplicates cmdctx.NonInteractiveEnv for core callers, which
// cannot import the cli layer; the two sets are pinned equal by a test on the
// cmdctx side.
func NonInteractiveEnv() bool {
	v := os.Getenv("DWE_NONINTERACTIVE")
	return v == "1" || v == "true"
}
