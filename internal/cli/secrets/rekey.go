package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// rekeyResumeHint is the recovery instruction for a rekey that failed after the
// new keyfile was written. Both identities are on disk at that point and the
// tree is mixed, which is exactly the state every read path already handles: the
// configured identity is tried first and the straggler keyfiles after it, so a
// re-run converges instead of needing a manual repair.
const rekeyResumeHint = "both identities are on disk and every read tries them all, so the tree is readable; " +
	"run 'dwe secrets rekey' again to finish it, or 'dwe secrets status' to see what is left"

// rekeyJSON is the `dwe secrets rekey` payload: the retired and the new
// recipient, where the new identity landed, and what was rewritten.
type rekeyJSON struct {
	OldRecipient string   `json:"old_recipient"`
	Recipient    string   `json:"recipient"`
	Keyfile      string   `json:"keyfile"`
	Markers      int      `json:"markers"`
	Layers       []string `json:"layers"`
	Files        []string `json:"files"`
}

func newRekeyCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "rekey",
		Short: "Replace the project key pair, re-encrypting every secret",
		Long: `Mint a new age key pair and re-encrypt every committed secret to it.

Run this when the identity may have leaked, or when someone who held it should
no longer be able to read the project's secrets. Every ENC[age:…] marker in the
config layers and every *.age config-pack source is re-encrypted; the new
recipient is written to workspace.yml last.

This machine must be able to READ every existing secret, so run it where the
current identity lives. The whole tree is decrypted and validated in memory
first: a value this machine cannot open — or one whose YAML shape cannot be
rewritten in place — aborts the command with nothing written.

Recovery: the new keyfile is written before anything is re-encrypted and the old
one is kept, so an interrupted run leaves a readable — if mixed — tree that a
second 'dwe secrets rekey' finishes. Remove the old keyfile once every developer
has imported the new identity with 'dwe secrets key import'.`,
		Example:      `  dwe secrets rekey`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRekey(cmd, flags)
		},
	}
}

func runRekey(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	// Lock-held diagnostics go to stderr so JSON-mode stdout stays clean. No
	// preflight: re-encrypting touches no container and no stack state.
	w := render.NewWriter(cmd.ErrOrStderr())
	release, err := cmdctx.AcquireProjectLocksOrReport(flags.ProjectRoot(), w)
	if err != nil {
		return err
	}
	defer release()

	layers, err := loadRawLayers(flags)
	if err != nil {
		return err
	}
	oldRecipient, err := recipientOrErr(layers)
	if err != nil {
		return err
	}

	// Phase 1 — read-only. Everything is decrypted and validated into memory
	// before the first byte is written, so a corrupt marker or a file encrypted
	// to somebody else stops the command while the tree is still consistent.
	plan, err := planRekey(flags, layers, oldRecipient)
	if err != nil {
		return err
	}

	// Phase 2 — the new keyfile is the FIRST mutation. A recipient committed
	// without a readable identity would lock the project out of its own secrets,
	// whereas an extra keyfile is inert.
	newID, err := secrets.Keygen()
	if err != nil {
		return cmdctx.ErrWrap("secrets_keygen_failed", err)
	}
	keyfile, err := secrets.WriteKeyfile(newID)
	if err != nil {
		return cmdctx.ErrWrap("secrets_keyfile_write_failed", err).
			WithHint("remove or back up the conflicting keyfile, then run 'dwe secrets rekey' again")
	}
	recipient := newID.Recipient()

	// Phase 3 — re-encrypt the .age sources, each atomically.
	rewrittenFiles := make([]string, 0, len(plan.files))
	for _, f := range plan.files {
		data, eerr := secrets.EncryptBytes(f.plain, recipient)
		if eerr != nil {
			return rekeyWriteError(cmdctx.ErrWrap("secrets_encrypt_failed", eerr), f.path, flags.ProjectRoot())
		}
		if werr := writeFileAtomicMode(f.path, data, f.mode); werr != nil {
			return rekeyWriteError(cmdctx.ErrWrap("secrets_rekey_failed", werr), f.path, flags.ProjectRoot())
		}
		rewrittenFiles = append(rewrittenFiles, relToRoot(flags.ProjectRoot(), f.path))
	}

	// Phase 4 — rewrite the markers in each layer file through the splice writer,
	// so comments, anchors and quoting survive.
	rewrittenLayers := make([]string, 0, len(plan.layers))
	for _, l := range plan.layers {
		if _, werr := rekeyLayerFile(l, plan.plain, recipient); werr != nil {
			// A splice refusal keeps its own code, because the first step is to
			// reshape that value rather than to re-run blindly — but the keyfile
			// is already minted and part of the tree is already re-encoded, so
			// the resume half of the story has to travel with it.
			if unsupported, ok := rekeySpliceError(werr, relToRoot(flags.ProjectRoot(), l.path)); ok {
				return unsupported
			}
			return rekeyWriteError(cmdctx.ErrWrap("secrets_rekey_failed", werr), l.path, flags.ProjectRoot())
		}
		rewrittenLayers = append(rewrittenLayers, relToRoot(flags.ProjectRoot(), l.path))
	}

	// Phase 5 — the recipient LAST: until it moves, the project still declares
	// the old key, which is the identity every already-rewritten value can no
	// longer be read with — but the straggler lookup covers exactly that gap.
	if err := writeRecipient(flags.ConfigPath, recipient); err != nil {
		if unsupported, ok := rekeySpliceError(err, relToRoot(flags.ProjectRoot(), flags.ConfigPath)); ok {
			return unsupported
		}
		return rekeyWriteError(cmdctx.ErrWrap("secrets_recipient_write_failed", err), flags.ConfigPath, flags.ProjectRoot())
	}

	data := rekeyJSON{
		OldRecipient: oldRecipient,
		Recipient:    recipient,
		Keyfile:      keyfile,
		Markers:      plan.markers,
		Layers:       rewrittenLayers,
		Files:        rewrittenFiles,
	}
	return cmdctx.WriteData(flags, cmd, data, func(d rekeyJSON) string {
		var b strings.Builder
		b.WriteString("re-encrypted to a new age key pair\n")
		fmt.Fprintf(&b, "  recipient: %s (was %s)\n", d.Recipient, d.OldRecipient)
		fmt.Fprintf(&b, "  keyfile:   %s\n", d.Keyfile)
		fmt.Fprintf(&b, "  rewritten: %d marker(s) in %d layer file(s), %d encrypted file(s)\n\n",
			d.Markers, len(d.Layers), len(d.Files))
		b.WriteString("Commit the rewritten files and the new secrets.recipient.\n")
		b.WriteString("Share the new identity with 'dwe secrets key export', then remove the old keyfile\n")
		b.WriteString("for " + d.OldRecipient + " once everyone has imported it.")
		return b.String()
	})
}

// rekeyPlan is the read-only pass's result: every plaintext, keyed by the marker
// it came from, plus the files that will have to be rewritten.
type rekeyPlan struct {
	plain   map[string]string // marker text → plaintext
	layers  []rekeyLayer
	files   []rekeyFile
	markers int
}

// rekeyLayer is one config layer file carrying at least one marker, with the
// node-writer label and mode policy that file needs.
type rekeyLayer struct {
	path   string
	label  string
	policy localpkg.WritePolicy
}

// rekeyFile is one *.age source, already decrypted, with the mode to restore.
type rekeyFile struct {
	path  string
	plain []byte
	mode  os.FileMode
}

// planRekey decrypts and validates the whole encrypted surface into memory. Any
// failure here aborts before the first mutation, which is what makes phase 2's
// keyfile write the point of no return rather than a gamble.
func planRekey(flags *cmdctx.RootFlags, layers []config.Layer, recipient string) (rekeyPlan, error) {
	ids := keygate.LoadIdentitySet(recipient)
	root := flags.ProjectRoot()
	plan := rekeyPlan{plain: make(map[string]string)}

	seen := make(map[string]bool)
	// CollectMarkers is ordered by layer then path, so the layer list below comes
	// out in load order and the report is stable across runs.
	for _, m := range config.CollectMarkers(layers) {
		plan.markers++
		if _, ok := plan.plain[m.Value]; !ok {
			value, err := ids.Decrypt(m.Value)
			if err != nil {
				return rekeyPlan{}, rekeyReadError(recipient, relToRoot(root, m.Layer)+":"+m.Path, err)
			}
			plan.plain[m.Value] = value
		}
		if !seen[m.Layer] {
			seen[m.Layer] = true
			label, policy := layerWritePolicy(m.Layer, layers)
			plan.layers = append(plan.layers, rekeyLayer{path: m.Layer, label: label, policy: policy})
		}
	}

	files, err := keygate.CollectAgeFiles(root)
	if err != nil {
		return rekeyPlan{}, cmdctx.ErrWrap("secrets_rekey_blocked", err)
	}
	for _, f := range files {
		display := relToRoot(root, f.Path)
		if f.Err != nil {
			return rekeyPlan{}, cmdctx.ErrWrap("secrets_rekey_blocked", f.Err).
				WithDetail("file", display).
				WithHint("nothing was written; a rekey cannot rewrite a source it must not read")
		}
		data, rerr := os.ReadFile(f.Path)
		if rerr != nil {
			return rekeyPlan{}, cmdctx.ErrWrap("secrets_rekey_blocked", rerr).WithDetail("file", display)
		}
		plain, derr := ids.DecryptBytes(data)
		if derr != nil {
			return rekeyPlan{}, rekeyReadError(recipient, display, derr)
		}
		mode := ciphertextMode
		if fi, serr := os.Stat(f.Path); serr == nil {
			mode = fi.Mode().Perm()
		}
		plan.files = append(plan.files, rekeyFile{path: f.Path, plain: plain, mode: mode})
	}

	if err := dryRunSplices(plan, root, flags.ConfigPath, recipient); err != nil {
		return rekeyPlan{}, err
	}
	return plan, nil
}

// dryRunSplices proves on throwaway Splicers that every edit phases 4 and 5 will
// attempt can actually be spliced. Decryptability alone is not enough: a marker
// written as a block scalar or inside a flow mapping decrypts fine and only
// refuses at splice time — which would abort AFTER the new keyfile was minted
// and every .age source re-encrypted to it, leaving a tree whose ciphertext is
// sealed to a key pair that was never exported. Committing that tree locks every
// other developer out of those files, and the shape is detectable read-only, so
// it belongs in phase 1 with the rest of the validation.
//
// The probe value is each marker re-encrypted to the CURRENT recipient: age
// ciphertext length depends on the plaintext, not on which X25519 recipient it
// is sealed to, so the probe splices exactly like the real replacement will.
func dryRunSplices(plan rekeyPlan, root, configPath, recipient string) error {
	for _, l := range plan.layers {
		splicer, err := localpkg.NewSplicer(l.path, l.label)
		if err != nil {
			return rekeyPlanSpliceError(err, relToRoot(root, l.path))
		}
		if _, err := splicer.ReplaceScalars(func(s string) (string, bool, error) {
			value, ok := plan.plain[s]
			if !secrets.IsMarker(s) || !ok {
				return s, false, nil
			}
			probe, eerr := secrets.Encrypt(value, recipient)
			if eerr != nil {
				return s, false, eerr
			}
			return probe, true, nil
		}); err != nil {
			return rekeyPlanSpliceError(err, relToRoot(root, l.path))
		}
	}

	// Phase 5's target. A recipient is a fixed-length age1… string, so rewriting
	// it with the current one probes that site at its real width.
	splicer, err := localpkg.NewSplicer(configPath, localpkg.LabelWorkspace)
	if err != nil {
		return rekeyPlanSpliceError(err, relToRoot(root, configPath))
	}
	if err := splicer.SetScalar([]string{"secrets", "recipient"}, recipient); err != nil {
		return rekeyPlanSpliceError(err, relToRoot(root, configPath))
	}
	return nil
}

// rekeyPlanSpliceError decorates a refusal the read-only dry run found. Unlike
// rekeySpliceError it carries `written: false` and no resume text: no key pair
// has been minted and nothing re-encoded, so reshaping the value and re-running
// is the whole recovery.
func rekeyPlanSpliceError(err error, file string) error {
	if unsupported, ok := spliceUnsupportedError(err, file); ok {
		return unsupported.WithDetail("written", false)
	}
	return cmdctx.ErrWrap("secrets_rekey_blocked", err).
		WithDetail("file", file).
		WithDetail("written", false).
		WithHint("nothing was written; " + file + " could not be prepared for an in-place rewrite")
}

// rekeyLayerFile re-encrypts every marker in one layer file in place through the
// splice writer, so the diff is one line per marker and nothing else in the file
// moves. A file whose markers all live behind aliases (rewritten once at the
// anchor) yields zero replacements and is not touched.
//
// The callback's error channel aborts the whole file with its bytes unchanged:
// a marker that cannot be re-encrypted must not leave the layer half-rewritten.
func rekeyLayerFile(l rekeyLayer, plain map[string]string, recipient string) (int, error) {
	splicer, err := localpkg.NewSplicer(l.path, l.label)
	if err != nil {
		return 0, err
	}

	count, err := splicer.ReplaceScalars(func(s string) (string, bool, error) {
		if !secrets.IsMarker(s) {
			return s, false, nil
		}
		value, ok := plain[s]
		if !ok {
			// Only reachable if the file changed under the lock between the
			// read-only pass and now.
			return s, false, fmt.Errorf("%s: an encrypted value appeared after the read-only pass", l.path)
		}
		marker, eerr := secrets.Encrypt(value, recipient)
		if eerr != nil {
			return s, false, eerr
		}
		return marker, true, nil
	})
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	if err := splicer.Write(l.path, l.policy); err != nil {
		return 0, err
	}
	return count, nil
}

// layerWritePolicy names a layer file for the splice writer and picks its mode
// policy. local.yml is gitignored developer state and stays FORCED to 0600; the
// two tracked files keep whatever mode the repository gave them, because forcing
// 0600 on a tracked file would surprise git and editors.
//
// workspace.yml is identified by position rather than by name (--config may
// point anywhere); the two overlay layers are located by LoadRawLayers at fixed
// names, so matching those by base name is exact.
func layerWritePolicy(path string, layers []config.Layer) (string, localpkg.WritePolicy) {
	switch {
	case len(layers) > 0 && layers[0].Path == path:
		return localpkg.LabelWorkspace, localpkg.PreserveOrDefault(0o644)
	case filepath.Base(path) == "local.yml":
		return localpkg.LabelLocal, localpkg.ForceMode(0o600)
	default:
		return localpkg.LabelDefaults, localpkg.PreserveOrDefault(0o644)
	}
}

// writeFileAtomicMode writes data through a temp file in the same directory and
// renames it into place, so an interrupted rekey leaves either the old
// ciphertext or the new one — never a truncated file nobody can decrypt.
func writeFileAtomicMode(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	// The suffix stays off .age so a leftover temp file from a crashed write is
	// never picked up by the config-pack scan.
	f, err := os.CreateTemp(dir, ".dwe-secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// rekeyReadError explains why the read-only pass could not open a value, naming
// it. Nothing has been written at this point, and the message says so.
func rekeyReadError(recipient, what string, err error) error {
	if errors.Is(err, secrets.ErrNoIdentity) {
		return identityError(recipient, err).
			WithDetail("value", what).
			WithDetail("written", false)
	}
	return cmdctx.ErrWrap("secrets_rekey_blocked", err).
		WithDetail("value", what).
		WithDetail("written", false).
		WithHint("nothing was written; run 'dwe secrets status' to see which values this machine cannot open")
}

// rekeyWriteError decorates a failure that happened after the new keyfile was
// installed with the resume instruction, so the mixed tree reads as recoverable
// rather than as damage.
func rekeyWriteError(err *cmdctx.CodedError, path, root string) error {
	return err.WithDetail("file", relToRoot(root, path)).WithHint(rekeyResumeHint)
}

// rekeySpliceError is spliceUnsupportedError for a refusal that lands in phase 4
// or 5 — after the new keyfile exists and part of the tree is re-encoded. The
// refusal keeps its own code and its reshape hint, because reshaping the value
// is the first thing to do; the resume text is appended rather than substituted,
// since without it the message reads as "nothing happened" while the tree is in
// fact mixed, and reshape-then-rerun is what actually finishes the rekey.
func rekeySpliceError(err error, file string) (error, bool) {
	unsupported, ok := spliceUnsupportedError(err, file)
	if !ok {
		return nil, false
	}
	return unsupported.
		WithDetail("written", true).
		WithHint(unsupported.Hint + "; " + rekeyResumeHint), true
}
