package lifecycle

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// resetEjectStdoutTarget is the --out value that emits to stdout instead of
// creating a file. Without it, `--out -` would create a file literally named
// "-". Same value and reason as `dwe deploy eject`.
const resetEjectStdoutTarget = "-"

// resetEjectCodePrefix namespaces the error codes the shared output-file helpers
// build (reset_eject_path_invalid, _output_exists, _output_invalid,
// _output_write_failed), so this command's JSON envelope never carries the
// secrets codes the helpers were extracted from.
const resetEjectCodePrefix = "reset_eject"

// resetEjectJSON is the `--output json` payload of a successful --out write. The
// stdout path has no envelope: there, the document itself is the payload.
type resetEjectJSON struct {
	Path     string `json:"path"`
	Pipeline string `json:"pipeline"`
}

// newResetEjectCmd creates `dwe reset eject`, which emits the built-in reset
// pipeline as an authorable reset.yml. Deliberately parallel to
// `dwe deploy eject` (internal/cli/deploy/eject.go) down to the wording: the
// only difference between the two commands should be the pipeline.
func newResetEjectCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var out string
	var force bool

	cmd := &cobra.Command{
		Use:   "eject",
		Short: "Print the built-in reset pipeline as an editable reset.yml",
		Long: `Emit the built-in default reset pipeline as a commented, editable reset.yml.

This is the pipeline that runs when the project has no workspace/reset.yml (or
one that is empty / all comments) — it is a constant, not this project's
effective plan: per-service pipelines are not inlined, nothing is rendered, and
there is no --service filter. Use 'dwe reset plan' for the resolved instance.

With no --out the document goes to stdout. With --out PATH it is written to that
file, refusing to overwrite an existing one unless --force is given. The
canonical target is workspace/reset.yml; pass it explicitly.

Once workspace/reset.yml is active it REPLACES the built-in pipeline whole.
There is no lifecycle equivalent of this command: the effective stop pipeline
carries the engine-synthetic _auto_reap_daemons phase, and an emitted
lifecycle.yml declaring it would not load back.`,
		Example: `  dwe reset eject
  dwe reset eject --out workspace/reset.yml
  dwe reset eject --out workspace/reset.yml --force`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResetEject(cmd, flags, out, cmd.Flags().Changed("out"), force)
		},
	}

	// --out, never --output: the root command owns -o for --output, and a second
	// shorthand would shadow it. Same reason `dwe secrets` and `dwe docs
	// llms-txt` spell it --out.
	cmd.Flags().StringVar(&out, "out", "", "write the document to PATH instead of stdout ('"+resetEjectStdoutTarget+"' means stdout)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing output file")
	// Cobra file-completes a string flag on its own; a custom ValidArgsFunction
	// touching project state would pull in the CompletionConfigPath obligation
	// for no gain.
	_ = cmd.MarkFlagFilename("out", "yml", "yaml")
	return cmd
}

// runResetEject emits the built-in reset pipeline. No preflight and no project
// locks: this writes an authoring file, never stack state.
func runResetEject(cmd *cobra.Command, flags *cmdctx.RootFlags, out string, outSet, force bool) error {
	doc := reset.DefaultResetYAML()

	// Bare command and `--out -` are the same path: the document, and nothing
	// else, on stdout — in json mode too, where the document is the payload.
	if !outSet || out == resetEjectStdoutTarget {
		if _, err := cmd.OutOrStdout().Write(doc); err != nil {
			return cmdctx.ErrWrap(resetEjectCodePrefix+"_output_write_failed", err)
		}
		return nil
	}

	dst, err := cmdctx.ResolveFilePath(resetEjectCodePrefix, flags.ProjectRoot(), out, "output")
	if err != nil {
		return err
	}
	if err := cmdctx.WriteOutputFile(resetEjectCodePrefix, cmdctx.OutputFile{
		Path: dst,
		Data: doc,
		Mode: 0o644,
		// An ejected pipeline is an ordinary source file: never re-permission
		// one the repository already owns.
		TightenMode: false,
		Force:       force,
		ExistsNote:  existingResetNote(flags.ProjectRoot(), dst),
	}); err != nil {
		return err
	}

	if flags.Output == "json" {
		return cmdctx.WriteJSON(flags, cmd, resetEjectJSON{Path: dst, Pipeline: "reset"})
	}
	render.NewWriter(cmd.ErrOrStderr()).
		Success(fmt.Sprintf("Wrote the built-in reset pipeline to %s", dst))
	return nil
}

// existingResetNote explains why an existing reset.yml matters, for the refusal
// message. A file that does not load — a syntax error, an unknown field — gets
// no note on purpose: the command still refuses, but as "a file is already
// here", never by propagating a parse error as if it were a write failure.
//
// Only the project's own workspace/reset.yml gets a note: the sentence claims
// the built-in default "is what runs today", which is true of the project's
// pipeline, not of an arbitrary --out target that dwe never reads.
func existingResetNote(projectRoot, path string) string {
	if !cmdctx.IsCanonicalPipelinePath(projectRoot, "reset.yml", path) {
		return ""
	}
	cfg, state, err := config.LoadResetConfigWithState(path)
	if err != nil || cfg == nil {
		return ""
	}
	return cmdctx.InertPipelineNote(state, len(cfg.Phases), "reset")
}
