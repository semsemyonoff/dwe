package compose

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"

	"github.com/spf13/cobra"
)

// allFlagUsage is the --all help text shared by every compose subcommand; the
// opening mirrors `dwe docker pull|build --all`.
const allFlagUsage = "use all configured overlays, not just enabled ones " +
	"(includes overlays of disabled services; for inspection only: the combined " +
	"chain may not be valid if disabled overlays conflict)"

// errBareWithAll rejects `raw --bare --all`: --bare emits no -f files at all,
// so there is no chain for --all to widen.
var errBareWithAll = errors.New("--bare and --all are mutually exclusive: --bare passes no -f files, so there is no chain for --all to widen")

// NewCmd builds the `dwe compose` command tree.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "compose",
		Short:        "Low-level Docker Compose diagnostics",
		GroupID:      groupID,
		SilenceUsage: true,
	}
	cmd.AddCommand(newComposeFilesCmd(flags))
	cmd.AddCommand(newComposeRawCmd(flags))
	cmd.AddCommand(newComposeArgvCmd(flags))
	return cmd
}

// composeFilesFor returns the -f chain: every configured overlay when all is
// set, otherwise the active chain (enabled overlays only) that lifecycle
// commands use.
func composeFilesFor(cfg *config.DweConfig, all bool) []string {
	if all {
		return cfg.ComposeFilesAll()
	}
	return cfg.ComposeFiles()
}

// newComposeRawCmd creates the `dwe compose raw` command.
// It resolves the compose file list and project name from config, then delegates
// to `docker compose` with the user-supplied arguments. All docker compose flags
// and subcommands can be passed after `--`.
//
// With --bare, only the project name is injected (no default compose files).
// This is useful for commands that use a standalone compose file like the installer.
// With --all, the -f chain also includes overlays of disabled services.
func newComposeRawCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "raw [--bare | --all] [-- docker-compose-args...]",
		Short: "Run docker compose directly with resolved file list and project name (escape hatch)",
		Long: `Run docker compose directly with the resolved -f file list and project name.
No docker.yml policy args are applied; the remaining arguments go to docker
compose unchanged:

	dwe compose raw -- ps -a
	dwe compose raw --all -- config

--bare and --all are dwe's own flags up to the first docker compose argument,
a single leading -- included: "dwe compose raw -- --all config" widens the -f
chain, while "dwe compose raw -- ps --all" passes --all to docker compose.
--bare and --all are mutually exclusive. Every other argument, --help included,
goes to docker compose; "dwe help compose raw" shows this help.`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// DisableFlagParsing is on: dwe's own flags are parsed by hand.
			opts, passArgs, err := parseRawFlags(args)
			if err != nil {
				return err
			}

			cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}

			// Use docker policy project name for consistency with dwe docker commands.
			baseDir := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
			if err != nil {
				return err
			}

			// Resolve the project name the same way every other path does
			// (docker.yml project_name -> else "<prefix>-<name>") so this escape
			// hatch targets the same compose project even when docker.yml is
			// absent — passing the raw (possibly empty) field would emit `-p ""`.
			composeProject, err := config.ResolveComposeProjectName(baseDir, cfg)
			if err != nil {
				return err
			}
			composeArgs := buildRawComposeArgs(cfg, composeProject, opts, passArgs)

			dockerCmd := exec.Command(config.DockerBin(cfg), append([]string{"compose"}, composeArgs...)...) //nolint:gosec
			dockerCmd.Dir = baseDir
			dockerCmd.Env = docker.MergeEnv(dockerCfg.ProcessEnv)
			dockerCmd.Stdin = cmd.InOrStdin()
			dockerCmd.Stdout = cmd.OutOrStdout()
			dockerCmd.Stderr = cmd.ErrOrStderr()
			return dockerCmd.Run()
		},
		SilenceUsage: true,
	}
	// Declared so they show up in `dwe help compose raw`. With
	// DisableFlagParsing cobra never parses them (and `raw --help` reaches
	// docker compose); parseRawFlags does.
	cmd.Flags().Bool("bare", false, "inject only the project name, no -f files (for a standalone compose file)")
	cmd.Flags().Bool("all", false, allFlagUsage)
	return cmd
}

// rawFlags holds the dwe-level flags of `dwe compose raw`.
type rawFlags struct {
	bare bool
	all  bool
}

// buildRawComposeArgs assembles the `docker compose` argv (without the leading
// "compose") for `dwe compose raw`.
func buildRawComposeArgs(cfg *config.DweConfig, composeProject string, opts rawFlags, passArgs []string) []string {
	var composeArgs []string
	if composeProject != "" {
		composeArgs = append(composeArgs, "-p", composeProject)
	}
	if !opts.bare {
		for _, f := range composeFilesFor(cfg, opts.all) {
			composeArgs = append(composeArgs, "-f", f)
		}
	}
	return append(composeArgs, passArgs...)
}

// parseRawFlags extracts --bare and --all from the arg list and returns the
// remaining args with the leading "--" separator stripped. dwe flags are
// recognized in any position up to the first token that is neither a dwe flag
// nor that single leading "--" (so "-- --bare" still sets bare, as it always
// has); from that token on everything — "--bare", "--all" and further "--"
// included — passes through to docker compose unchanged.
func parseRawFlags(args []string) (opts rawFlags, rest []string, err error) {
	separatorSkipped := false
	positionalStarted := false
	for _, arg := range args {
		switch {
		case positionalStarted:
			rest = append(rest, arg)
		case arg == "--bare":
			opts.bare = true
		case arg == "--all":
			opts.all = true
		case arg == "--" && !separatorSkipped:
			separatorSkipped = true
		default:
			positionalStarted = true
			rest = append(rest, arg)
		}
	}
	if opts.bare && opts.all {
		return rawFlags{}, nil, errBareWithAll
	}
	return opts, rest, nil
}

// newComposeArgvCompose returns the Compose whose argv `dwe compose argv`
// prints: built from every configured overlay when all is set.
func newComposeArgvCompose(cfg *config.DweConfig, dockerCfg *config.DockerConfig, baseDir string, all bool) *docker.Compose {
	if all {
		return docker.NewComposeAll(cfg, dockerCfg, baseDir)
	}
	return docker.NewCompose(cfg, dockerCfg, baseDir)
}

// newComposeArgvCmd creates the `dwe compose argv` command.
// It shows the full `docker compose` command that `dwe docker <command>` would
// execute, without running it. Useful for diagnostics and debugging.
func newComposeArgvCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "argv [--all] <command> [args...]",
		Short: "Show the full docker compose command that would be executed",
		Long: `Show the full docker compose command that dwe docker <command> would execute,
without running it. --all is recognized only before <command>; everything from
<command> on goes to docker compose, so "dwe compose argv --all ps" widens the
-f chain while "dwe compose argv ps --all" passes --all to docker compose. The
first standalone -- is dropped, so "dwe compose argv up -- -d" prints "up -d".`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// args[0] is never "--" here: pflag consumes a "--" that precedes
			// <command>, so MinimumNArgs(1) still guarantees the command.
			args = dropFirstSeparator(args, cmd.ArgsLenAtDash())

			cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}

			baseDir := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
			if err != nil {
				return err
			}

			compose := newComposeArgvCompose(cfg, dockerCfg, baseDir, all)

			command := args[0]
			extraArgs := args[1:]
			fullArgs := compose.BuildArgs(command, extraArgs...)

			// Print as: <bin> compose -p ... -f ... <command> ...
			_, err = fmt.Fprintln(cmd.OutOrStdout(), compose.BinName()+" "+strings.Join(fullArgs, " "))
			return err
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&all, "all", false, allFlagUsage)
	// Flags after <command> belong to docker compose, as with `dwe docker`:
	// `argv exec app ls --all` must print --all, not widen the chain.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// dropFirstSeparator removes the first standalone "--" from args unless pflag
// already consumed one (argsLenAtDash >= 0). With interspersed parsing off,
// pflag stops at <command> and leaves a later "--" in args; before, it always
// swallowed the first one, and `argv up -- -d` printed "up -d". Dropping it
// keeps that output for every invocation that was valid before.
func dropFirstSeparator(args []string, argsLenAtDash int) []string {
	if argsLenAtDash >= 0 {
		return args
	}
	i := slices.Index(args, "--")
	if i < 0 {
		return args
	}
	return slices.Delete(slices.Clone(args), i, i+1)
}

func newComposeFilesCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "files [--all]",
		Short: "Print resolved compose file list (base + enabled overlays, or every overlay with --all), one per line",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}
			for _, f := range composeFilesFor(cfg, all) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), f)
			}
			return nil
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&all, "all", false, allFlagUsage)
	return cmd
}
