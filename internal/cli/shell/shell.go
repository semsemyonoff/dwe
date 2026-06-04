package shell

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
//   - If exactly one enabled service exists, it is auto-selected.
//   - If multiple enabled services exist, the selector function is called.
//   - If no enabled services exist, an error is returned.
//
// "Enabled" means mandatory or explicitly enabled in the current config.
func pickService(cfg *config.DweConfig, serviceName string, selector selectServiceFn) (string, error) {
	if serviceName != "" {
		return serviceName, nil
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
(e.g. 'bash -c "<command>"') and dwe exits with the command's exit code. TTY is
allocated only when both stdin and stdout are terminals, so piping works cleanly.
dwe's own connection banners are suppressed so the child's stdout is untouched.`,
		Example: `  dwe shell
  dwe shell main
  dwe shell main --root
  dwe shell main --mode run --shell sh
  dwe shell main --user deploy --workdir /app
  dwe shell main -c "composer install"
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

			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
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
			serviceName, err := pickService(cfg, argName, svcSelector)
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
	cmd.GroupID = groupID
	return cmd
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
}
