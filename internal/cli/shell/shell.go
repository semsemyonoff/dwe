package shell

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/services"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/docker"

	"maps"

	"github.com/spf13/cobra"
)

// validModes contains the allowed values for the --mode flag.
var validModes = map[string]bool{"auto": true, "exec": true, "run": true}

// selectServiceFn is the function signature for interactive service selection.
// It receives the sorted list of enabled service names and returns the chosen name.
type selectServiceFn func(cfg *config.DweConfig, names []string) (string, error)

// defaultSelectService shows an interactive selector via widgets.RunSelector.
func defaultSelectService(cfg *config.DweConfig, names []string) (string, error) {
	items := make([]widgets.SelectorItem, len(names))
	for i, name := range names {
		svc := cfg.Services[name]
		items[i] = widgets.SelectorItem{
			Label:       name,
			Description: svc.Container,
		}
	}
	idx, err := widgets.RunSelector("Select a service:", items)
	if err != nil {
		return "", err
	}
	return names[idx], nil
}

// pickService resolves which service name to use for the shell command.
//   - If serviceName is non-empty, it is returned directly.
//   - Else, if cwd is inside a service's source directory, that service is used.
//   - Else, if exactly one enabled service exists, it is auto-selected.
//   - Else, if multiple enabled services exist, the selector function is called.
//   - Else (no enabled services), an error is returned.
//
// "Enabled" means mandatory or explicitly enabled in the current config. The
// cwd match deliberately ignores enabled state: navigating into a service's
// folder is an explicit target, like passing its name.
func pickService(cfg *config.DweConfig, serviceName, root, cwd string, selector selectServiceFn) (string, error) {
	if serviceName != "" {
		return serviceName, nil
	}

	// "I'm standing in this service's folder" — treat as an explicit target.
	if detected := services.DetectByCwd(cfg.Services, root, cwd); detected != "" {
		return detected, nil
	}

	// Collect enabled services in sorted order.
	var enabled []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		svc := cfg.Services[name]
		if svc.Required || svc.Enabled {
			enabled = append(enabled, name)
		}
	}

	switch len(enabled) {
	case 0:
		return "", fmt.Errorf("no enabled services — enable a service with 'dwe services enable <name>'")
	case 1:
		return enabled[0], nil
	default:
		return selector(cfg, enabled)
	}
}

// NewCmd builds the `dwe shell` cobra command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var asRoot bool
	var flagMode string
	var flagShell string
	var flagUser string
	var flagWorkDir string
	var flagEnvVars []string
	var flagCommand string
	var flagTTY bool
	var flagNoTTY bool

	cmd := &cobra.Command{
		Use:   "shell [service]",
		Short: "Open a shell in a service container",
		Long: `Open an interactive shell in the specified service container.

Mode controls how the shell is opened (--mode auto|exec|run):
  auto  — connect via 'docker exec' if running, 'compose run' if absent, error if stopped (default)
  exec  — always use 'docker exec'; error if container is not running
  run   — always start a new container via 'docker compose run --rm'

Shell, user, working directory, and env defaults are read from the service
cli config block in workspace/services/<name>/service.yml and can be overridden with flags.

When no service argument is given, the command auto-selects if only one enabled
service exists, or shows an interactive selector when multiple services are enabled.

With -c "<command>", the command is evaluated by the container's resolved shell
(e.g. 'bash -c "<command>"') and dwe exits with the command's exit code.
dwe's own connection banners are suppressed so the child's stdout is untouched.

PTY allocation: an interactive shell gets one when both stdin and stdout are
terminals; -c never gets one by default, so piping stays byte-clean. The cost of
that default is buffering — a child whose stdout is a pipe switches to block
buffering, so a long-running command prints nothing until it exits. Pass --tty
to force a PTY and get incremental output back, at the price of \n becoming
\r\n. --no-tty forces the opposite. --tty cannot be honoured when the shell has
to start a new container (--mode run) while stdin is not a terminal: compose
refuses to allocate a PTY there, and the command says so.`,
		Example: `  dwe shell
  dwe shell main
  dwe shell main --root
  dwe shell main --mode run --shell sh
  dwe shell main --user deploy --workdir /app
  dwe shell main -c "composer install"
  dwe shell main -c "npm run build" --tty
  dwe shell main -c "php artisan migrate" --mode run`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: cmdctx.ServiceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate mutual exclusion: --root and --user cannot both be set.
			if asRoot && flagUser != "" {
				return fmt.Errorf("--root and --user are mutually exclusive")
			}
			// Validate --mode value.
			if flagMode != "" && !validModes[flagMode] {
				return fmt.Errorf("--mode must be one of: auto, exec, run (got %q)", flagMode)
			}
			// Validate -c/--command: explicit empty/whitespace-only string is a usage error.
			if cmd.Flags().Changed("command") && strings.TrimSpace(flagCommand) == "" {
				return fmt.Errorf("-c/--command cannot be empty or whitespace-only")
			}
			// Validate mutual exclusion: --tty and --no-tty contradict each other.
			if flagTTY && flagNoTTY {
				return fmt.Errorf("--tty and --no-tty are mutually exclusive")
			}

			cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}
			baseDir := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
			if err != nil {
				return err
			}
			compose := docker.NewCompose(cfg, dockerCfg, baseDir)

			argName := ""
			if len(args) > 0 {
				argName = args[0]
			}
			svcSelector := selectServiceFn(defaultSelectService)
			if !widgets.IsInteractiveFn(cmd.InOrStdin()) {
				svcSelector = func(_ *config.DweConfig, _ []string) (string, error) {
					return "", fmt.Errorf("multiple services are enabled; pass a service name or run in an interactive terminal")
				}
			}
			cwd, _ := os.Getwd()
			serviceName, err := pickService(cfg, argName, baseDir, cwd, svcSelector)
			if err != nil {
				if errors.Is(err, widgets.ErrCancelled) {
					return nil
				}
				return err
			}

			shellFlags := shellCLIFlags{
				asRoot:  asRoot,
				mode:    flagMode,
				shell:   flagShell,
				user:    flagUser,
				workDir: flagWorkDir,
				envVars: flagEnvVars,
				command: flagCommand,
				tty:     resolveTTYMode(flagTTY, flagNoTTY),
			}
			processEnv := compose.BuildEnv()
			dockerBin := compose.BinName()
			return dispatchShell(cfg, compose, serviceName, shellFlags, processEnv, dockerBin)
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "run as root user")
	cmd.Flags().StringVar(&flagMode, "mode", "", "shell mode: auto, exec, or run")
	cmd.Flags().StringVar(&flagShell, "shell", "", "shell binary to use (e.g. bash, sh, zsh)")
	cmd.Flags().StringVar(&flagUser, "user", "", "user to run as inside the container")
	cmd.Flags().StringVar(&flagWorkDir, "workdir", "", "working directory inside the container")
	cmd.Flags().StringArrayVar(&flagEnvVars, "env", nil, "set an environment variable (KEY=VALUE); overrides service cli.env config")
	cmd.Flags().StringVarP(&flagCommand, "command", "c", "", "run a single command via `<shell> -c \"…\"` and exit (non-interactive)")
	cmd.Flags().BoolVarP(&flagTTY, "tty", "t", false, "force a pseudo-TTY (keeps long-running output unbuffered when stdout is a pipe)")
	cmd.Flags().BoolVar(&flagNoTTY, "no-tty", false, "never allocate a pseudo-TTY, even on an interactive terminal")
	cmd.GroupID = groupID
	return cmd
}

// resolveTTYMode maps the two boolean flags onto the tri-state ttyMode. The
// mutually-exclusive case is rejected in RunE before this is reached.
func resolveTTYMode(force, off bool) ttyMode {
	switch {
	case force:
		return ttyOn
	case off:
		return ttyOff
	default:
		return ttyAuto
	}
}

// shellCLIFlags holds the flag values passed to the shell command.
type shellCLIFlags struct {
	asRoot  bool
	mode    string
	shell   string
	user    string
	workDir string
	envVars []string // KEY=VALUE pairs from --env flags; override service cli.env config
	command string   // non-empty triggers one-shot mode (`<shell> -c "<command>"`)
	tty     ttyMode  // --tty / --no-tty; zero value keeps the auto-detect default
}
