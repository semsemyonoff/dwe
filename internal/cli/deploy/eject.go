package deploy

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// ejectStdoutTarget is the --out value that emits to stdout instead of creating
// a file. Without it, `--out -` would create a file literally named "-".
const ejectStdoutTarget = "-"

// ejectCodePrefix namespaces the error codes the shared output-file helpers
// build (deploy_eject_path_invalid, _output_exists, _output_invalid,
// _output_write_failed), so this command's JSON envelope never carries the
// secrets codes the helpers were extracted from.
const ejectCodePrefix = "deploy_eject"

// ejectJSON is the `--output json` payload of a successful --out write. The
// stdout path has no envelope: there, the document itself is the payload.
type ejectJSON struct {
	Path     string `json:"path"`
	Pipeline string `json:"pipeline"`
}

// newDeployEjectCmd creates `dwe deploy eject`, which emits the built-in deploy
// pipeline as an authorable deploy.yml.
//
// Deliberately not part of the interactive `dwe deploy` menu (menu.go): that
// menu offers deploy-execution actions, while this one authors a config file.
func newDeployEjectCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var out string
	var force bool

	cmd := &cobra.Command{
		Use:   "eject",
		Short: "Print the built-in deploy pipeline as an editable deploy.yml",
		Long: `Emit the built-in default deploy pipeline as a commented, editable deploy.yml.

This is the pipeline that runs when the project has no workspace/deploy.yml (or
one that is empty / all comments) — it is a constant, not this project's
effective plan: per-service pipelines are not inlined, nothing is rendered, and
there is no --service filter. Use 'dwe deploy plan' for the resolved instance.

With no --out the document goes to stdout. With --out PATH it is written to that
file, refusing to overwrite an existing one unless --force is given. The
canonical target is workspace/deploy.yml; pass it explicitly.

Once workspace/deploy.yml is active it REPLACES the built-in pipeline whole.
There is no lifecycle equivalent of this command: the effective stop pipeline
carries the engine-synthetic _auto_reap_daemons phase, and an emitted
lifecycle.yml declaring it would not load back.`,
		Example: `  dwe deploy eject
  dwe deploy eject --out workspace/deploy.yml
  dwe deploy eject --out workspace/deploy.yml --force`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployEject(cmd, flags, out, cmd.Flags().Changed("out"), force)
		},
	}

	// --out, never --output: the root command owns -o for --output, and a second
	// shorthand would shadow it. Same reason `dwe secrets` and `dwe docs
	// llms-txt` spell it --out.
	cmd.Flags().StringVar(&out, "out", "", "write the document to PATH instead of stdout ('"+ejectStdoutTarget+"' means stdout)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing output file")
	// Cobra file-completes a string flag on its own; a custom ValidArgsFunction
	// touching project state would pull in the CompletionConfigPath obligation
	// for no gain.
	_ = cmd.MarkFlagFilename("out", "yml", "yaml")
	return cmd
}

// runDeployEject emits the built-in deploy pipeline. No preflight and no
// project locks: this writes an authoring file, never stack state.
func runDeployEject(cmd *cobra.Command, flags *cmdctx.RootFlags, out string, outSet, force bool) error {
	doc := deploy.DefaultDeployYAML()

	// Bare command and `--out -` are the same path: the document, and nothing
	// else, on stdout — in json mode too, where the document is the payload.
	if !outSet || out == ejectStdoutTarget {
		if _, err := cmd.OutOrStdout().Write(doc); err != nil {
			return cmdctx.ErrWrap(ejectCodePrefix+"_output_write_failed", err)
		}
		return nil
	}

	dst, err := cmdctx.ResolveFilePath(ejectCodePrefix, flags.ProjectRoot(), out, "output")
	if err != nil {
		return err
	}
	if err := cmdctx.WriteOutputFile(ejectCodePrefix, cmdctx.OutputFile{
		Path: dst,
		Data: doc,
		Mode: 0o644,
		// An ejected pipeline is an ordinary source file: never re-permission
		// one the repository already owns.
		TightenMode: false,
		Force:       force,
		ExistsNote:  existingDeployNote(flags.ProjectRoot(), dst),
	}); err != nil {
		return err
	}

	if flags.Output == "json" {
		return cmdctx.WriteJSON(flags, cmd, ejectJSON{Path: dst, Pipeline: "deploy"})
	}
	sharedrender.NewWriter(cmd.ErrOrStderr()).
		Success(fmt.Sprintf("Wrote the built-in deploy pipeline to %s", dst))
	return nil
}

// existingDeployNote explains why an existing deploy.yml matters, for the
// refusal message. A file that does not load — a syntax error, an unknown field
// — gets no note on purpose: the command still refuses, but as "a file is
// already here", never by propagating a parse error as if it were a write
// failure.
//
// Only the project's own workspace/deploy.yml gets a note: the sentence claims
// the built-in default "is what runs today", which is true of the project's
// pipeline, not of an arbitrary --out target that dwe never reads.
func existingDeployNote(projectRoot, path string) string {
	if !cmdctx.IsCanonicalPipelinePath(projectRoot, "deploy.yml", path) {
		return ""
	}
	cfg, state, err := config.LoadProjectDeployConfigWithState(path)
	if err != nil || cfg == nil {
		return ""
	}
	return cmdctx.InertPipelineNote(state, len(cfg.Phases), "deploy")
}
