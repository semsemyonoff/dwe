package vars

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	uirender "github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// runAsk is the package-level test seam over ask.Run (mirroring the runAsk seam
// in internal/cli/command). Tests stub it to drive the no-value interactive form
// deterministically; they MUST NOT call t.Parallel() while overriding it
// (global state).
var runAsk = ask.Run

// varSetJSON is the JSON shape for `dwe vars set --output json`: the var and its
// effective value after the write.
type varSetJSON struct {
	Var   string `json:"var"`
	Value any    `json:"value"`
}

func newVarsSetCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <var> [value]",
		Short: "Write a var override to workspace/local.yml",
		Long: `Set a var by writing an override to workspace/local.yml, preserving the
file's comments and formatting.

The value is parsed as a single YAML scalar: true / false coerce to bool, 42 to
int, 1.5 to float; quote the argument (e.g. '"42"') to force a string. Maps and
sequences are rejected — a var is a leaf value.

With no value an interactive form opens (TTY only); in JSON or non-interactive
mode a missing value is an error.

The vars. prefix is optional ("db.host" writes vars.db.host). From inside a
container the target must additionally be listed in the project's
bridge.vars_writable allowlist; from the host, set is unrestricted.`,
		Example: `  dwe vars set db.host db.internal
  dwe vars set db.port 5432
  dwe vars set feature.enabled true
  dwe vars set db.host          # interactive form`,
		Args:              cobra.RangeArgs(1, 2),
		SilenceUsage:      true,
		ValidArgsFunction: leafCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The vars. prefix is optional — "db.host" writes "vars.db.host".
			path := normalizeVarPath(args[0])
			var rawValue string
			var haveValue bool
			if len(args) == 2 {
				rawValue, haveValue = args[1], true
			}
			_, err := runVarsSet(cmd, flags, path, rawValue, haveValue)
			return err
		},
	}
	return cmd
}

// runVarsSet validates and (after coercion) writes a var override to local.yml.
// The value source is either the positional arg, the interactive form, or — in
// JSON / non-interactive mode with no value — a typed vars_value_required error.
//
// committed reports whether a write actually happened: it is false when the
// interactive form was aborted (a clean no-op) and on every error, and true once
// the override is persisted. The TUI browser uses it to decide whether to close
// (committed) or reopen (aborted) after an edit.
func runVarsSet(cmd *cobra.Command, flags *cmdctx.RootFlags, path, rawValue string, haveValue bool) (committed bool, err error) {
	// Path confinement: vars.* only. This is also the container trust boundary —
	// a non-vars path could otherwise mutate formalized config. The
	// container-write allowlist gate lives at the shared write chokepoint
	// (writeVarOverrideCore) so every entry point — this CLI path and the in-TUI
	// edit overlay alike — is covered structurally, not per-caller.
	if err := validateVarsSetPath(path); err != nil {
		return false, err
	}

	// Resolve the value: positional arg, interactive form, or value-required.
	if !haveValue {
		if flags.Output == "json" || cmdctx.NonInteractiveEnv() || !widgets.IsInteractiveFn(cmd.InOrStdin()) {
			return false, cmdctx.Err("vars_value_required",
				fmt.Sprintf("a value is required for %q (no interactive form in this mode)", path)).
				WithDetail("var", path)
		}
		v, ok, ferr := promptForVarValue(cmd, flags, path)
		if ferr != nil {
			return false, ferr
		}
		if !ok {
			// User aborted the form — a clean no-op, mirroring `commands` run.
			return false, nil
		}
		rawValue = v
	}

	coerced, err := varsusage.CoerceScalar(rawValue)
	if err != nil {
		return false, cmdctx.ErrWrap("vars_value_invalid", err).WithDetail("var", path)
	}

	newCfg, err := writeVarOverride(cmd, flags, path, coerced)
	if err != nil {
		return false, err
	}

	// The write is persisted from here on, so committed is true even if emitting
	// the confirmation fails.
	effective, _ := varsusage.ResolveVar(newCfg, path)
	data := varSetJSON{Var: path, Value: effective}
	return true, cmdctx.WriteData(flags, cmd, data, func(d varSetJSON) string {
		rendered, _ := uirender.VarValue(d.Value)
		return uirender.VarSetConfirmation(d.Var, strings.TrimRight(rendered, "\n"))
	})
}

// validateVarsSetPath rejects any path not strictly beneath the vars. head: a
// non-vars first segment, the bare `vars` namespace, or any empty segment.
func validateVarsSetPath(path string) error {
	parts := strings.Split(path, ".")
	if parts[0] != varsusage.VarsPrefix || len(parts) < 2 {
		return cmdctx.Err("vars_path_invalid",
			fmt.Sprintf("path %q must be a vars.* leaf (e.g. vars.db.host)", path)).
			WithDetail("var", path)
	}
	if slices.Contains(parts, "") {
		return cmdctx.Err("vars_path_invalid",
			fmt.Sprintf("path %q has an empty segment", path)).
			WithDetail("var", path)
	}
	return nil
}

// buildVarOverride builds the nested overlay map for a vars dot-path: e.g.
// "vars.db.host" + value → {vars: {db: {host: value}}}.
func buildVarOverride(path string, value any) map[string]any {
	parts := strings.Split(path, ".")
	root := make(map[string]any)
	node := root
	for i, p := range parts {
		if i == len(parts)-1 {
			node[p] = value
			break
		}
		child := make(map[string]any)
		node[p] = child
		node = child
	}
	return root
}

// writeVarOverride performs the locked write for the CLI path: it acquires
// project locks via the PRINTING wrapper (lock-held diagnostics go to stderr so
// JSON-mode stdout stays clean) and delegates to writeVarOverrideCore. Symmetry
// with the services toggle, which shares this writer/file.
func writeVarOverride(cmd *cobra.Command, flags *cmdctx.RootFlags, path string, value any) (*config.DweConfig, error) {
	baseDir := flags.ProjectRoot()

	// Lock-held diagnostics go to stderr so JSON-mode stdout stays clean.
	w := render.NewWriter(cmd.ErrOrStderr())
	release, err := cmdctx.AcquireProjectLocksOrReport(baseDir, w)
	if err != nil {
		return nil, err
	}
	defer release()

	return writeVarOverrideCore(flags, path, value)
}

// writeVarOverrideSilent performs the same locked write as writeVarOverride but
// via the SILENT lock wrapper: nothing is printed, and a held-lock error is
// returned unchanged for the caller to surface itself. It is the in-TUI edit
// path — the alt-screen is live, so stderr diagnostics would corrupt the frame;
// the caller renders the returned error as a status flash instead.
func writeVarOverrideSilent(flags *cmdctx.RootFlags, path string, value any) (*config.DweConfig, error) {
	baseDir := flags.ProjectRoot()

	release, err := cmdctx.AcquireProjectLocksSilent(baseDir)
	if err != nil {
		return nil, err
	}
	defer release()

	return writeVarOverrideCore(flags, path, value)
}

// writeVarOverrideCore is the shared, lock-agnostic write body: capture
// pre-state for rollback, apply the overlay onto the loaded local.yml node
// (preserving comments), write atomically, and reload config. No preflight —
// this is not a lifecycle/stack mutation. On any post-write failure the captured
// bytes are restored. Callers MUST already hold the project locks.
func writeVarOverrideCore(flags *cmdctx.RootFlags, path string, value any) (*config.DweConfig, error) {
	// Container-write gate: from inside a container the target var must match a
	// bridge.vars_writable pattern (host writes are unrestricted). Enforced here,
	// at the single write chokepoint, so both the CLI `set` path and the in-TUI
	// edit overlay are covered regardless of entry point. The top-level command
	// allowlist (bridgepolicy) is prefix-wide and cannot see the var arg, so this
	// runtime check is the real read/write boundary.
	if bridgeclient.InContainer() {
		cfg, err := loadConfigForVars(flags)
		if err != nil {
			return nil, err
		}
		if !config.VarsWritableAllows(config.BridgeVarsWritable(cfg), path) {
			return nil, cmdctx.Err("vars_not_container_writable",
				fmt.Sprintf("var %q is not writable from inside a container", path)).
				WithDetail("var", path).
				WithHint("add it to bridge.vars_writable, or run `dwe vars set` on the host")
		}
	}

	localPath := filepath.Join(flags.ProjectRoot(), "workspace", "local.yml")

	captured, err := captureLocalState(localPath)
	if err != nil {
		return nil, fmt.Errorf("capturing local.yml state: %w", err)
	}

	overlay := buildVarOverride(path, value)
	doc, err := localpkg.LoadLocalYAMLNode(localPath)
	if err != nil {
		return nil, err
	}
	if err := localpkg.ApplyOverlayToNode(doc, overlay); err != nil {
		return nil, err
	}
	if err := localpkg.WriteLocalYAMLNode(localPath, doc); err != nil {
		restoreLocalState(localPath, captured)
		return nil, err
	}

	newCfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		restoreLocalState(localPath, captured)
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	return newCfg, nil
}

// buildVarSetFields builds the single-input field slice for a var `set` form,
// shared by the standalone `dwe vars set <path>` no-value prompt (via runAsk)
// and the in-TUI vars-browser edit overlay (via ask.Build). The field carries
// the inspect-style per-layer description and an inline CoerceScalar validator
// so an invalid scalar (a map / sequence, or anything CoerceScalar rejects) is
// caught IN-FORM rather than only after submit — an improvement over the old
// post-submit-only coercion. The caller's post-submit CoerceScalar remains the
// authoritative parse.
func buildVarSetFields(flags *cmdctx.RootFlags, path string) []ask.Field {
	disp := uirender.DisplayVarPath(path)
	return []ask.Field{{
		Key:         "value",
		Title:       "New value for " + disp,
		Description: varSetFormDescription(flags, path),
		Kind:        ask.FieldInput,
		Validate: func(s string) error {
			_, err := varsusage.CoerceScalar(s)
			return err
		},
	}}
}

// promptForVarValue opens the single-input huh form for a no-value set,
// carrying inspect-style per-layer info as the field description. It returns the
// submitted value; ok is false when the user aborts (widgets.ErrCancelled) — the
// caller treats that as a clean no-op.
func promptForVarValue(cmd *cobra.Command, flags *cmdctx.RootFlags, path string) (value string, ok bool, err error) {
	disp := uirender.DisplayVarPath(path)
	res, ferr := runAsk(context.Background(), "dwe vars › set "+disp, buildVarSetFields(flags, path),
		ask.RunOptions{Input: cmd.InOrStdin(), Output: cmd.OutOrStdout()})
	if ferr != nil {
		if errors.Is(ferr, widgets.ErrCancelled) {
			return "", false, nil
		}
		return "", false, ferr
	}
	return res.String("value"), true, nil
}

// varSetFormDescription builds the inspect-style help text shown under the
// form input: the current per-layer values for the var. Resolution failures
// degrade to an empty description (the form still works).
func varSetFormDescription(flags *cmdctx.RootFlags, path string) string {
	layered, err := config.ResolveLayeredPath(flags.ConfigPath, path)
	if err != nil {
		return ""
	}
	var b strings.Builder
	if layered.DefaultOK {
		fmt.Fprintf(&b, "default: %s\n", inlineFormValue(layered.Default))
	}
	if layered.LocalOK {
		fmt.Fprintf(&b, "local:   %s\n", inlineFormValue(layered.Local))
	}
	if layered.CurrentOK {
		fmt.Fprintf(&b, "current: %s", inlineFormValue(layered.Current))
	} else {
		b.WriteString("current: (unset)")
	}
	return b.String()
}

// inlineFormValue renders a scalar value compactly for the form description.
func inlineFormValue(v any) string {
	if v == nil {
		return "null"
	}
	rendered, err := uirender.VarValue(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimRight(rendered, "\n")
}

// captureLocalState returns the current bytes of localPath, or nil when absent.
func captureLocalState(localPath string) ([]byte, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// restoreLocalState restores localPath to its captured bytes (best-effort). A
// nil capture (file was absent) removes the file.
func restoreLocalState(localPath string, captured []byte) {
	if captured == nil {
		_ = os.Remove(localPath)
		return
	}
	_ = os.WriteFile(localPath, captured, 0o600)
}
