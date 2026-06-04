// Package scaffold provides the `dwe init` command: it scaffolds a fresh DWE
// project from nothing. This package is the cli/ layer for the command — it owns
// flag parsing, interactive-vs-non-interactive mode selection, the huh form, and
// result reporting. All file-system work lives in the domain package
// internal/core/workflow/scaffold; this package is the only writer to
// stdout/stderr.
//
// The package and command name diverge by necessity: the cobra command is
// `Use: "init"`, but `init` is a reserved function name and an illegal package
// name in Go, so the package is named `scaffold` (matching the domain package).
package scaffold

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	core "github.com/semsemyonoff/dwe/internal/core/workflow/scaffold"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
)

// defaultPrefix is the compose/project prefix used when none is supplied.
const defaultPrefix = "dwe"

// defaultService is the starter service folder created unless --service is
// overridden (an explicit empty value scaffolds no service).
const defaultService = "app"

// formInput carries the values a form collects (and the flag-derived defaults
// that pre-fill it). It is the seam between flag/option plumbing and the huh
// form so the interactive path can be swapped out in tests.
type formInput struct {
	Name     string
	Prefix   string
	Branding core.Branding
}

// runFormFn runs the interactive form. It is a package-level var so tests can
// replace it — exercising the abort path and the value-mapping path without
// driving a real huh form (which requires a TTY). The default implementation
// builds ask.Fields and delegates to ask.Run.
var runFormFn = func(ctx context.Context, in formInput, stdin io.Reader, stdout io.Writer) (formInput, error) {
	fields := []ask.Field{
		{
			Key:      "name",
			Kind:     ask.FieldInput,
			Title:    "Project name",
			Required: true,
			Default:  in.Name,
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("project name is required")
				}
				return nil
			},
		},
		{
			Key:     "prefix",
			Kind:    ask.FieldInput,
			Title:   "Compose prefix",
			Default: in.Prefix,
		},
		{
			Key:         "brand_title",
			Kind:        ask.FieldInput,
			Title:       "Branding title (optional)",
			Description: "Shown in the project header; leave blank for a generic header",
			Default:     in.Branding.Title,
		},
		{
			Key:     "tagline",
			Kind:    ask.FieldInput,
			Title:   "Tagline (optional)",
			Default: in.Branding.Tagline,
		},
		{
			Key:     "accent",
			Kind:    ask.FieldInput,
			Title:   "Accent color (optional)",
			Default: in.Branding.Accent,
		},
	}

	res, err := ask.Run(ctx, "dwe init", fields, ask.RunOptions{Input: stdin, Output: stdout})
	if err != nil {
		return formInput{}, err
	}

	return formInput{
		Name:   strings.TrimSpace(res.String("name")),
		Prefix: strings.TrimSpace(res.String("prefix")),
		Branding: core.Branding{
			Title:   strings.TrimSpace(res.String("brand_title")),
			Tagline: strings.TrimSpace(res.String("tagline")),
			Accent:  strings.TrimSpace(res.String("accent")),
		},
	}, nil
}

// initFlags collects the raw flag values bound by NewCmd.
type initFlags struct {
	name       string
	prefix     string
	brandTitle string
	tagline    string
	accent     string
	service    string
	force      bool
	yes        bool
}

// NewCmd builds the `dwe init` command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	f := initFlags{}

	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Scaffold a new DWE project",
		Long: `Scaffold a fresh DWE project in the current directory (or ./<name>/ when a
name is given). Interactive by default; falls back to flag-driven defaults when
stdin/stdout is not a TTY, or when --yes / --output json is set.

The command fills gaps and never overwrites existing files unless --force is set,
so it is safe to re-run on a partially set-up project.`,
		Example: `  dwe init
  dwe init my-project
  dwe init --name my-project --prefix acme --service api
  dwe init --yes --output json`,
		Args:         cobra.MaximumNArgs(1),
		GroupID:      groupID,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, flags, args, f)
		},
	}

	cmd.Flags().StringVar(&f.name, "name", "", "project name (default: [name] arg or current directory name)")
	cmd.Flags().StringVar(&f.prefix, "prefix", defaultPrefix, "compose/project prefix")
	cmd.Flags().StringVar(&f.brandTitle, "brand-title", "", "branding title written to workspace/styles.yml")
	cmd.Flags().StringVar(&f.tagline, "tagline", "", "branding tagline written to workspace/styles.yml")
	cmd.Flags().StringVar(&f.accent, "accent", "", "branding accent color written to workspace/styles.yml")
	cmd.Flags().StringVar(&f.service, "service", defaultService, `starter service folder name ("" creates none)`)
	cmd.Flags().BoolVar(&f.force, "force", false, "overwrite existing files instead of skipping them")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "skip the interactive form and take all defaults")

	return cmd
}

// runInit resolves the scaffold Options (from flags, positional arg, and the
// interactive form when applicable), calls the domain Scaffold, and reports the
// result. The form — when run — is collected in full before any disk write, so a
// mid-form Ctrl-C leaves the disk untouched.
func runInit(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, f initFlags) error {
	// Target dir: a positional name creates ./<name>/; otherwise the cwd.
	targetDir := ""
	if len(args) > 0 {
		targetDir = args[0]
	}

	name, err := resolveName(f.name, args)
	if err != nil {
		return err
	}

	in := formInput{
		Name:   name,
		Prefix: f.prefix,
		Branding: core.Branding{
			Title:   f.brandTitle,
			Tagline: f.tagline,
			Accent:  f.accent,
		},
	}

	if interactive(flags, f.yes) {
		collected, ferr := runFormFn(cmd.Context(), in, cmd.InOrStdin(), cmd.OutOrStdout())
		if ferr != nil {
			if errors.Is(ferr, huh.ErrUserAborted) {
				// Aborted before any write — clean exit, nothing on disk.
				return nil
			}
			return cmdctx.ErrWrap("scaffold_form_failed", ferr)
		}
		in = collected
	}

	opts := core.Options{
		TargetDir: targetDir,
		Name:      strings.TrimSpace(in.Name),
		Prefix:    strings.TrimSpace(in.Prefix),
		Service:   f.service,
		Branding:  in.Branding,
		Force:     f.force,
	}
	if opts.Prefix == "" {
		opts.Prefix = defaultPrefix
	}
	if opts.Name == "" {
		return cmdctx.Err("scaffold_invalid_name", "project name is required").
			WithHint("pass a name argument, --name, or run interactively")
	}

	res, err := core.Scaffold(opts)
	if err != nil {
		return cmdctx.ErrWrap("scaffold_failed", err)
	}

	return writeResult(flags, cmd, res)
}

// resolveName resolves the project name: --name wins, then the positional [name]
// arg, then the current directory's base name.
func resolveName(nameFlag string, args []string) (string, error) {
	if strings.TrimSpace(nameFlag) != "" {
		return strings.TrimSpace(nameFlag), nil
	}
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return filepath.Base(strings.TrimSpace(args[0])), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", cmdctx.ErrWrap("scaffold_cwd", err)
	}
	return filepath.Base(cwd), nil
}

// interactive reports whether the form should be shown: a real TTY on both ends,
// not --yes, and not JSON output mode.
func interactive(flags *cmdctx.RootFlags, yes bool) bool {
	return widgets.IsInteractiveFn(os.Stdin) && !yes && flags.Output != "json"
}

// initJSON is the machine-readable result shape for --output json.
type initJSON struct {
	Target          string   `json:"target"`
	Created         []string `json:"created"`
	Skipped         []string `json:"skipped"`
	SymlinkFallback bool     `json:"symlink_fallback"`
	NestedWarning   bool     `json:"nested_warning"`
}

// writeResult renders the scaffold Result in the active output mode.
func writeResult(flags *cmdctx.RootFlags, cmd *cobra.Command, res core.Result) error {
	dto := initJSON{
		Target:          res.Target,
		Created:         orEmpty(res.Created),
		Skipped:         orEmpty(res.Skipped),
		SymlinkFallback: res.SymlinkFallback,
		NestedWarning:   res.NestedWarning,
	}
	return cmdctx.WriteData(flags, cmd, dto, renderInitText)
}

// renderInitText renders the human-facing summary: a nested-project warning (if
// any), grouped created/skipped lists, a symlink-fallback note, and a
// next-steps footer.
func renderInitText(d initJSON) string {
	var b strings.Builder

	if d.NestedWarning {
		b.WriteString(styles.WarningStyle().Render(
			"warning: an ancestor workspace.yml was found — this project is nested inside another DWE project"))
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "Initialized DWE project at %s\n", styles.StyleKey(d.Target))

	if len(d.Created) > 0 {
		b.WriteString("\n")
		b.WriteString(styles.SuccessStyle().Render("Created:"))
		b.WriteString("\n")
		for _, p := range d.Created {
			fmt.Fprintf(&b, "  + %s\n", p)
		}
	}

	if len(d.Skipped) > 0 {
		b.WriteString("\n")
		b.WriteString(styles.StyleMuted("Skipped (already present):"))
		b.WriteString("\n")
		for _, p := range d.Skipped {
			fmt.Fprintf(&b, "  · %s\n", styles.StyleMuted(p))
		}
	}

	if d.SymlinkFallback {
		b.WriteString("\n")
		b.WriteString(styles.StyleMuted(
			"note: CLAUDE.md was written as a copy of AGENTS.md (symlinks unavailable on this platform)"))
		b.WriteString("\n")
	}

	b.WriteString("\nNext steps:\n")
	b.WriteString("  1. review workspace.yml and workspace/defaults.yml\n")
	b.WriteString("  2. run `dwe validate` to check the project\n")
	b.WriteString("  3. run `dwe deploy run` to bring the stack up")

	return b.String()
}

// orEmpty returns s, or an empty (non-nil) slice when s is nil, so JSON output
// always emits `[]` rather than `null`.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
