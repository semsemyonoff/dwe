package secrets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// runAsk is the package-level test seam over ask.Run (mirroring the seam in
// internal/cli/vars). Tests stub it to drive the hidden prompt
// deterministically; they MUST NOT call t.Parallel() while overriding it.
var runAsk = ask.Run

// The two layer files `secrets set` may target. local.yml is deliberately not
// one of them: it is gitignored developer state, so encrypting a value there
// buys nothing — `dwe vars set` is the right tool for a personal value.
const (
	fileDefaults  = "defaults"
	fileWorkspace = "workspace"
	fileLocal     = "local"
)

// secretSetJSON is the `dwe secrets set` payload: what was encrypted and which
// layer file now carries it (relative to the project root, like every other
// layer reference in this tree).
type secretSetJSON struct {
	Path string `json:"path"`
	File string `json:"file"`
}

func newSetCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		fromStdin bool
		file      string
	)
	cmd := &cobra.Command{
		Use:   "set <vars.path> [value]",
		Short: "Encrypt a value into a committed config layer",
		Long: `Encrypt a value to the project's recipient and write it as an ENC[age:…]
marker into a committed config layer.

The value comes from the positional argument, from --stdin, or — with neither,
on a terminal — from a hidden prompt. A value passed as an argument lands in the
shell history like any other argument; prefer --stdin or the prompt for anything
that matters.

The path must live under vars.: that is the only free-form sandbox in the config
schema, and the only place the overlay validator lets an arbitrary value live.
The value is always stored as a string — no true/42/1.5 coercion, so a secret
that looks like a number stays what you typed.

Encryption needs only the committed recipient, so anyone with the repository can
add a secret; reading one back needs the private identity.

Writes to workspace/defaults.yml by default (--file workspace targets
workspace.yml). workspace/local.yml is refused: it is gitignored personal state,
where 'dwe vars set' already stores a plaintext value.`,
		Example: `  dwe secrets set vars.telegram.token 123:abc
  pbpaste | dwe secrets set vars.telegram.token --stdin
  dwe secrets set vars.telegram.token          # hidden prompt`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			value := ""
			haveValue := false
			if len(args) == 2 {
				value, haveValue = args[1], true
			}
			return runSet(cmd, flags, args[0], value, haveValue, fromStdin, file)
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "read the value from stdin (one trailing newline is trimmed)")
	cmd.Flags().StringVar(&file, "file", fileDefaults, "layer file to write: defaults|workspace")
	return cmd
}

func runSet(cmd *cobra.Command, flags *cmdctx.RootFlags, path, value string, haveValue, fromStdin bool, file string) error {
	if err := validateSecretPath(path); err != nil {
		return err
	}
	target, err := resolveTargetFile(flags.ConfigPath, file)
	if err != nil {
		return err
	}

	// The value is resolved BEFORE the locks: a hidden prompt can sit open for
	// as long as the developer needs, and holding the project locks meanwhile
	// would block every other dwe command in the project.
	plain, ok, err := resolveSetValue(cmd, flags, path, value, haveValue, fromStdin)
	if err != nil {
		return err
	}
	if !ok {
		// The prompt was aborted: a clean no-op, mirroring `dwe vars set`.
		return nil
	}

	// Lock-held diagnostics go to stderr so JSON-mode stdout stays clean. No
	// preflight: writing a marker touches no container and no stack state.
	w := render.NewWriter(cmd.ErrOrStderr())
	release, err := cmdctx.AcquireProjectLocksOrReport(flags.ProjectRoot(), w)
	if err != nil {
		return err
	}
	defer release()

	// Re-read the layers under the lock: the recipient this value is encrypted
	// to must be the one committed right now, not the one a concurrent `rekey`
	// was retiring when the command started.
	layers, err := loadRawLayers(flags)
	if err != nil {
		return err
	}
	recipient, err := recipientOrErr(layers)
	if err != nil {
		return err
	}
	marker, err := secrets.Encrypt(plain, recipient)
	if err != nil {
		return cmdctx.ErrWrap("secrets_encrypt_failed", err)
	}

	if err := writeMarker(target, path, marker, layers); err != nil {
		return err
	}

	data := secretSetJSON{Path: path, File: relToRoot(flags.ProjectRoot(), target)}
	return cmdctx.WriteData(flags, cmd, data, func(d secretSetJSON) string {
		return fmt.Sprintf("%s encrypted into %s — commit it.", d.Path, d.File)
	})
}

// validateSecretPath confines the write to the vars. sandbox. Unlike `dwe vars
// set` the prefix is NOT optional here: silently rewriting `project.name` into
// `vars.project.name` would accept a path the user meant literally.
func validateSecretPath(path string) error {
	parts := strings.Split(path, ".")
	if parts[0] != varsusage.VarsPrefix || len(parts) < 2 {
		return cmdctx.Err("secrets_path_invalid",
			fmt.Sprintf("path %q must be a vars.* leaf (e.g. vars.telegram.token)", path)).
			WithDetail("path", path).
			WithHint("vars: is the only free-form block in the config schema; formalized fields cannot hold a secret")
	}
	if slices.Contains(parts, "") {
		return cmdctx.Err("secrets_path_invalid",
			fmt.Sprintf("path %q has an empty segment", path)).
			WithDetail("path", path)
	}
	return nil
}

// resolveTargetFile maps the --file choice onto a layer path. local.yml is
// refused by name rather than falling through to "unknown value", so the answer
// carries the alternative instead of a list of accepted words.
func resolveTargetFile(workspacePath, file string) (string, error) {
	switch file {
	case fileDefaults:
		return filepath.Join(filepath.Dir(workspacePath), "workspace", "defaults.yml"), nil
	case fileWorkspace:
		return workspacePath, nil
	case fileLocal:
		return "", cmdctx.Err("secrets_file_invalid",
			"workspace/local.yml is gitignored personal state, so encrypting a value there protects nothing").
			WithDetail("file", file).
			WithHint("use 'dwe vars set' for a personal value, or --file defaults for a shared one")
	default:
		return "", cmdctx.Err("secrets_file_invalid",
			fmt.Sprintf("unknown --file %q", file)).
			WithDetail("file", file).
			WithHint("--file accepts 'defaults' (workspace/defaults.yml) or 'workspace' (workspace.yml)")
	}
}

// resolveSetValue produces the plaintext from the argument, stdin or the hidden
// prompt. ok is false only when the prompt was aborted.
func resolveSetValue(cmd *cobra.Command, flags *cmdctx.RootFlags, path, value string, haveValue, fromStdin bool) (plain string, ok bool, err error) {
	if haveValue && fromStdin {
		return "", false, cmdctx.Err("secrets_value_ambiguous",
			"pass the value either as an argument or on stdin, not both").
			WithDetail("path", path)
	}
	if haveValue {
		return value, true, nil
	}
	if fromStdin {
		data, rerr := io.ReadAll(cmd.InOrStdin())
		if rerr != nil {
			return "", false, cmdctx.ErrWrap("secrets_value_read_failed", rerr)
		}
		// Exactly one trailing newline is trimmed: `printf 'x\n' | …` and an
		// editor-saved file both mean "x", while a value that deliberately ends
		// in a blank line keeps it.
		return trimOneNewline(string(data)), true, nil
	}
	if flags.Output == "json" || cmdctx.NonInteractiveEnv() || !widgets.IsInteractiveFn(cmd.InOrStdin()) {
		return "", false, cmdctx.Err("secrets_value_required",
			fmt.Sprintf("a value is required for %q (no interactive prompt in this mode)", path)).
			WithDetail("path", path).
			WithHint("pass the value as an argument, or pipe it in with --stdin")
	}
	return promptForSecret(cmd, path)
}

// promptForSecret opens the masked single-input form. An abort surfaces as a
// clean no-op, like every other ask-driven prompt in the CLI.
func promptForSecret(cmd *cobra.Command, path string) (string, bool, error) {
	fields := []ask.Field{{
		Key:         "value",
		Title:       "Secret value for " + path,
		Description: "Encrypted before it is written; the typed characters are not echoed.",
		Kind:        ask.FieldPassword,
		Required:    true,
	}}
	res, err := runAsk(context.Background(), "dwe secrets › set "+path, fields,
		ask.RunOptions{Input: cmd.InOrStdin(), Output: cmd.OutOrStdout()})
	if err != nil {
		if errors.Is(err, widgets.ErrCancelled) {
			return "", false, nil
		}
		return "", false, err
	}
	return res.String("value"), true, nil
}

// trimOneNewline removes a single trailing line terminator (\n or \r\n).
func trimOneNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

// writeMarker sets the marker at path in the target layer file through the
// splice writer, staging the result first: the spliced bytes are decoded back
// into a layer map, swapped into the raw layer list and run through the loader's
// own root validation. A `set` can therefore never leave a layer the project
// would refuse to load afterwards.
//
// The splice writer rather than the node writer, because a `set` into a
// hand-annotated layer file must produce a one-line diff: only the bytes of the
// marker's own value token change, and indentation, blank lines, comments,
// anchors and merge keys elsewhere in the file are never re-encoded. A shape it
// cannot edit in place (a block scalar, a flow collection) is refused with
// `secrets_write_unsupported` and the file untouched.
//
// The target files are git-tracked, hence PreserveOrDefault(0644): forcing
// local.yml's 0600 on a tracked file would surprise git and editors, and the
// marker is ciphertext anyway.
func writeMarker(target, path, marker string, layers []config.Layer) error {
	label, policy := layerWritePolicy(target, layers)

	splicer, err := localpkg.NewSplicer(target, label)
	if err != nil {
		return spliceWriteError(err, target)
	}
	if err := splicer.SetScalar(strings.Split(path, "."), marker); err != nil {
		return spliceWriteError(err, target)
	}

	var data map[string]any
	if err := yaml.Unmarshal(splicer.Bytes(), &data); err != nil {
		return cmdctx.ErrWrap("secrets_write_failed", fmt.Errorf("parse the staged %s: %w", target, err))
	}
	if data == nil {
		data = make(map[string]any)
	}
	if err := config.ValidateLayerRoots(stageLayers(layers, target, data)); err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}

	if err := splicer.Write(target, policy); err != nil {
		return spliceWriteError(err, target)
	}
	return nil
}

// stageLayers returns the layer list with target's data replaced by the staged
// map. A target that is not in the list yet (defaults.yml did not exist) is
// inserted right after workspace.yml, which is where the loader reads it — the
// position matters, because ValidateLayerRoots accepts a secrets: block in the
// first layer only.
func stageLayers(layers []config.Layer, target string, data map[string]any) []config.Layer {
	out := make([]config.Layer, len(layers))
	copy(out, layers)
	for i := range out {
		if out[i].Path == target {
			out[i].Data = data
			return out
		}
	}
	staged := config.Layer{Path: target, Data: data}
	if len(out) == 0 {
		return []config.Layer{staged}
	}
	return slices.Insert(out, 1, staged)
}

// recipientOrErr reads secrets.recipient out of an already-loaded raw layer set,
// or returns the typed "run init first" refusal.
func recipientOrErr(layers []config.Layer) (string, error) {
	recipient := config.RecipientFromLayers(layers)
	if recipient == "" {
		return "", cmdctx.Err("secrets_no_recipient",
			"this project has no secrets.recipient in workspace.yml").
			WithHint("run 'dwe secrets init' to mint a project key pair")
	}
	return recipient, nil
}
