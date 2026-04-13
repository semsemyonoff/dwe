package command

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"

	"github.com/spf13/cobra"
)

// implicitEnvStep is always the first step of any deploy plan.
// It regenerates .env from the current config before any phase runs.
var implicitEnvStep = config.DeployStep{
	Name:        "render-env",
	Cmd:         "./bin/devbox render env -o .env",
	Description: "Generate .env from config (implicit first step)",
}

func newDeployCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "deploy",
		Short:        "Deploy pipeline commands",
		SilenceUsage: true,
	}
	cmd.AddCommand(newDeployPlanCmd(flags))
	cmd.AddCommand(newDeployRunCmd(flags))
	cmd.AddCommand(newDeployStepCmd(flags))
	cmd.AddCommand(newDeployConfigCmd(flags))
	return cmd
}

func newDeployPlanCmd(flags *rootFlags) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show resolved deploy plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			steps, err := resolveDeployPlan(cfg)
			if err != nil {
				return fmt.Errorf("resolving deploy plan: %w", err)
			}

			switch format {
			case "shell":
				printDeployPlanShell(steps, cmd.OutOrStdout())
			default:
				printDeployPlanTable(steps, render.Stdout())
			}
			return nil
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or shell")
	return cmd
}

// resolvedStep holds a deploy step together with the phase it belongs to,
// after when-condition filtering.
//
// runtimeWhen is non-empty when the step's When condition is a runtime expression
// (builtin predicate or cmd:). Such conditions are NOT evaluated at plan-resolution
// time — they are checked immediately before the step executes.
type resolvedStep struct {
	phase       config.DeployPhase
	step        config.DeployStep
	runtimeWhen string // copy of step.When when IsRuntime; empty otherwise
}

// resolveDeployPlan builds the ordered list of active steps from cfg.Deploy.
// The implicit .env generation step is always prepended as step 0 (no phase).
// Steps whose when condition evaluates to false are excluded.
func resolveDeployPlan(cfg *config.DevboxConfig) ([]resolvedStep, error) {
	// Implicit first step — no associated phase.
	implicit := resolvedStep{
		phase: config.DeployPhase{Name: "env", Description: "Environment"},
		step:  implicitEnvStep,
	}
	result := []resolvedStep{implicit}

	for _, phase := range cfg.Deploy.Phases {
		for _, step := range phase.Steps {
			if step.When != "" {
				if condition.IsRuntime(step.When) {
					// Runtime conditions (builtin predicates, cmd:) are evaluated
					// at step-execution time, not at plan-resolution time.
					result = append(result, resolvedStep{phase: phase, step: step, runtimeWhen: step.When})
					continue
				}
				// Go template condition — evaluate against config now.
				ok, err := tpl.EvalCondition(step.When, cfg)
				if err != nil {
					return nil, fmt.Errorf("evaluating when condition for step %s/%s: %w", phase.Name, step.Name, err)
				}
				if !ok {
					continue
				}
			}
			result = append(result, resolvedStep{phase: phase, step: step})
		}
	}

	return result, nil
}

// printDeployPlanTable prints the plan in human-readable table format.
func printDeployPlanTable(steps []resolvedStep, w *render.Writer) {
	lastPhase := ""
	for _, rs := range steps {
		if rs.phase.Name != lastPhase {
			phaseLine := rs.phase.Name
			if rs.phase.Description != "" {
				phaseLine = rs.phase.Name + ": " + rs.phase.Description
			}
			w.TableSubheader(phaseLine)
			lastPhase = rs.phase.Name
		}

		badge := stepBadge(rs.step)
		name := rs.step.Name
		desc := rs.step.Description
		cmd := stepCommand(rs.step)

		if desc != "" {
			w.Definition(badge+" "+name, desc, 2, "", "—")
		} else {
			w.Println("  " + badge + " " + name)
		}
		if cmd != "" {
			w.Println("        " + cmd)
		}
		if rs.runtimeWhen != "" {
			w.Println("        [when: " + rs.runtimeWhen + "]")
		}
	}
}

// printDeployPlanShell emits executable shell commands for each step to w.
// Prepends "set -e" so the pipeline aborts on any step failure.
// cmd: steps are emitted as-is; make: steps become "make <target>".
// After the implicit .env generation step, ". .env" is emitted so that
// variables exported by .env (PROJECT_PREFIX, PROJECT_NAME, etc.) are
// available to all subsequent steps in the generated script.
func printDeployPlanShell(steps []resolvedStep, w io.Writer) {
	_, _ = fmt.Fprintln(w, "set -e")
	for _, rs := range steps {
		if rs.runtimeWhen != "" {
			_, _ = fmt.Fprintf(w, "# when: %s\n", rs.runtimeWhen)
		}
		_, _ = fmt.Fprintln(w, stepCommand(rs.step))
		if rs.step.Name == implicitEnvStep.Name {
			_, _ = fmt.Fprintln(w, ". .env")
		}
	}
}

// newDeployRunCmd creates the `devbox deploy run` command.
// It executes the resolved deploy plan step by step, printing phase/step
// progress and success messages directly — without generating a shell script.
// Devbox status messages are teed to deploy.log. Child process output
// (docker, make) goes directly to os.Stdout/os.Stderr so TTY detection works.
func newDeployRunCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "run",
		Short:        "Execute the deploy plan",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			steps, err := resolveDeployPlan(cfg)
			if err != nil {
				return fmt.Errorf("resolving deploy plan: %w", err)
			}

			workDir := filepath.Dir(flags.configPath)
			logPath := filepath.Join(workDir, "deploy.log")

			logFile, err := os.Create(logPath)
			if err != nil {
				return fmt.Errorf("creating deploy log %s: %w", logPath, err)
			}
			defer func() { _ = logFile.Close() }()

			// Devbox messages go to both terminal and log file.
			// Child process output goes directly to os.Stdout (see execStep).
			tee := io.MultiWriter(os.Stdout, &ansiStripper{logFile})
			w := render.NewWriter(tee)
			totalSteps := len(steps)
			lastPhase := ""

			for i, rs := range steps {
				if rs.phase.Name != lastPhase {
					phaseLabel := rs.phase.Name
					if rs.phase.Description != "" {
						phaseLabel = rs.phase.Name + ": " + rs.phase.Description
					}
					w.Info("Phase: " + phaseLabel)
					lastPhase = rs.phase.Name
				}

				stepLabel := rs.step.Name
				if rs.step.Description != "" {
					stepLabel = rs.step.Name + ": " + rs.step.Description
				}
				w.Info(fmt.Sprintf("  [%d/%d] %s", i+1, totalSteps, stepLabel))

				if rs.runtimeWhen != "" {
					ok, err := condition.EvalRuntime(rs.runtimeWhen, workDir)
					if err != nil {
						return fmt.Errorf("evaluating when condition for %s/%s: %w", rs.phase.Name, rs.step.Name, err)
					}
					if !ok {
						w.Warning(fmt.Sprintf("  [%d/%d] Skipped: %s (when: %s)", i+1, totalSteps, rs.step.Name, rs.runtimeWhen))
						continue
					}
				}

				if stepErr := execStep(rs.step, workDir); stepErr != nil {
					w.Error(fmt.Sprintf("Deploy failed at phase %q, step %q", rs.phase.Name, rs.step.Name))
					w.Error("  " + stepErr.Error())
					w.Warning("Full output saved to: " + logPath)
					return ErrSilent
				}

				// After .env is regenerated, load it into the current process
				// environment so subsequent cmd: steps can reference its variables.
				if rs.step.Name == implicitEnvStep.Name {
					if err := sourceDotEnv(filepath.Join(workDir, ".env")); err != nil {
						return fmt.Errorf("sourcing .env: %w", err)
					}
				}

				w.Success(fmt.Sprintf("  [%d/%d] Done: %s", i+1, totalSteps, rs.step.Name))
			}

			w.Info("Deploy log saved to: " + logPath)
			return nil
		},
	}
}

// sourceDotEnv reads a .env file and sets each KEY=VALUE pair as an OS
// environment variable so that subsequent exec.Cmd calls (with Env: nil)
// inherit them. Blank lines and comments are skipped.
func sourceDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if err := os.Setenv(strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("setenv %s: %w", key, err)
		}
	}
	return nil
}

// stepBadge returns the display badge for a step: [cmd] or [make].
func stepBadge(step config.DeployStep) string {
	if step.Make != "" {
		return "[make]"
	}
	return "[cmd]"
}

// stepCommand returns the resolved shell command for a step.
// For make: steps, it returns "make -f Makefile <target>"; for cmd: steps it returns the raw command.
func stepCommand(step config.DeployStep) string {
	if step.Make != "" {
		return "make -f Makefile " + strings.TrimSpace(step.Make)
	}
	return strings.TrimSpace(step.Cmd)
}

// findStep looks up a step by "<phase>/<step>" address in the deploy config.
// Returns the phase and step if found, or an error if not.
func findStep(cfg *config.DevboxConfig, address string) (config.DeployPhase, config.DeployStep, error) {
	parts := strings.SplitN(address, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("invalid step address %q: expected <phase>/<step>", address)
	}
	phaseName, stepName := parts[0], parts[1]

	for _, phase := range cfg.Deploy.Phases {
		if phase.Name != phaseName {
			continue
		}
		for _, step := range phase.Steps {
			if step.Name == stepName {
				return phase, step, nil
			}
		}
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("step %q not found in phase %q", stepName, phaseName)
	}
	return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("phase %q not found", phaseName)
}

// ansiRe matches ANSI/VT100 escape sequences and bare carriage returns.
// Used to strip control codes before writing to a plain-text log file.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[a-zA-Z]|\r`)

// ansiStripper wraps an io.Writer, stripping ANSI escape sequences and carriage
// returns before writing. The original byte-count is always returned so callers
// never see a short-write error.
type ansiStripper struct{ w io.Writer }

func (s *ansiStripper) Write(p []byte) (int, error) {
	_, err := s.w.Write(ansiRe.ReplaceAll(p, nil))
	return len(p), err
}

// execStep executes a deploy step in workDir. For cmd: steps it runs the command via sh -c;
// for make: steps it runs make <target>. Output is attached to the current process
// stdin/stdout/stderr. workDir must be the project root so that relative paths in
// commands and Makefile resolution work correctly.
//
// Signal handling: the child inherits devbox's terminal foreground process group,
// so Ctrl+C (SIGINT) is delivered by the terminal directly to the entire group
// (devbox + make + shell + docker). Make recipes and shell traps handle Docker
// container cleanup. devbox suppresses its own default SIGINT handler while waiting
// so it does not exit before the child finishes cleanup. A second Ctrl+C restores
// the default handler, allowing the user to force-exit if cleanup hangs.
// execStep executes a single deploy step. Child process stdout and stderr are
// connected directly to os.Stdout/os.Stderr so TTY detection works correctly
// (docker compose progress renderers, build output, etc.).
func execStep(step config.DeployStep, workDir string) error {
	var cmd *exec.Cmd
	if step.Make != "" {
		cmd = exec.Command("make", "-f", "Makefile", strings.TrimSpace(step.Make))
	} else {
		cmd = exec.Command("sh", "-c", strings.TrimSpace(step.Cmd))
	}
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// No Setpgid: child stays in the terminal foreground process group so Ctrl+C
	// reaches child processes (make, shell, docker) directly from the terminal.

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	// Suppress devbox's own default SIGINT/SIGTERM handlers while the child runs.
	// The terminal already delivers the signal to the whole foreground process group;
	// shell traps inside Make recipes handle Docker resource cleanup.
	// After the first signal, restore defaults: a second Ctrl+C will force-exit devbox.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if _, ok := <-sigCh; ok {
			signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		}
	}()

	waitErr := cmd.Wait()
	signal.Stop(sigCh)
	close(sigCh)

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return fmt.Errorf("exit status %d", exitErr.ExitCode())
		}
		return waitErr
	}
	return nil
}

func newDeployStepCmd(flags *rootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "step <phase>/<step>",
		Short: "Run a single deploy step by <phase>/<step> address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			address := args[0]
			phase, step, err := findStep(cfg, address)
			if err != nil {
				return err
			}

			// Evaluate when condition.
			if step.When != "" {
				var (
					ok  bool
					err error
				)
				if condition.IsRuntime(step.When) {
					ok, err = condition.EvalRuntime(step.When, filepath.Dir(flags.configPath))
				} else {
					ok, err = tpl.EvalCondition(step.When, cfg)
				}
				if err != nil {
					return fmt.Errorf("evaluating when condition for %s: %w", address, err)
				}
				if !ok {
					render.Stdout().Warning(fmt.Sprintf("skipping step %s/%s: when condition is false (%s)", phase.Name, step.Name, step.When))
					return nil
				}
			}

			resolved := stepCommand(step)
			if dryRun {
				fmt.Println(resolved)
				return nil
			}

			return execStep(step, filepath.Dir(flags.configPath))
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved command without executing")
	return cmd
}

// newDeployConfigCmd creates the `devbox deploy config <service>` command.
// It reads ServiceConfig.Configs[] for the named service and copies each template
// config to <service.Dir>/<Dest> using the declared Mode. Dest paths are
// validated against the service directory to prevent path traversal.
func newDeployConfigCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config <service>",
		Short: "Copy template configs to service directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			serviceName := args[0]
			svc, ok := cfg.Services[serviceName]
			if !ok {
				return fmt.Errorf("service %q not found in config", serviceName)
			}
			if svc.Dir == "" {
				return fmt.Errorf("service %q: dir is not set", serviceName)
			}

			projectRoot := filepath.Dir(flags.configPath)
			destDir := filepath.Join(projectRoot, svc.Dir)

			w := render.Stdout()
			for _, cf := range svc.Configs {
				src := filepath.Join(projectRoot, cf.Src)
				dest := filepath.Join(destDir, cf.Dest)
				// Guard against path traversal: dest must remain inside destDir.
				cleanDestDir := filepath.Clean(destDir)
				cleanDest := filepath.Clean(dest)
				if cleanDest == cleanDestDir || !strings.HasPrefix(cleanDest, cleanDestDir+string(filepath.Separator)) {
					return fmt.Errorf("service %q: config dest %q escapes the service directory", serviceName, cf.Dest)
				}
				if err := copyConfigFile(src, dest, cf.Mode); err != nil {
					return fmt.Errorf("copying %s → %s: %w", cf.Src, dest, err)
				}
				w.Success(fmt.Sprintf("config %s → %s [%s]", cf.Src, dest, cf.Mode))
			}
			return nil
		},
		SilenceUsage: true,
	}
}

// copyConfigFile copies src to dest using the given mode:
//   - "default" — skip if dest already exists
//   - "replace" — overwrite unconditionally
//   - "update"  — merge new keys from src into dest without overwriting existing values
//
// The dest directory is created if it does not exist.
func copyConfigFile(src, dest, mode string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	srcData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source %s: %w", src, err)
	}

	switch mode {
	case "default":
		if _, err := os.Stat(dest); err == nil {
			// Destination exists — skip.
			return nil
		}
		return os.WriteFile(dest, srcData, 0o644)

	case "replace":
		return os.WriteFile(dest, srcData, 0o644)

	case "update":
		return updateEnvFile(srcData, dest)

	default:
		// Treat unknown mode as "default".
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		return os.WriteFile(dest, srcData, 0o644)
	}
}

// updateEnvFile merges new KEY=VALUE entries from srcData into the dest file.
// Keys already present in dest are preserved unchanged. New keys from the
// source template are appended to dest. If dest does not exist it is created
// with the full content of srcData.
func updateEnvFile(srcData []byte, dest string) error {
	destData, err := os.ReadFile(dest)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(dest, srcData, 0o644)
	}
	if err != nil {
		return fmt.Errorf("read dest %s: %w", dest, err)
	}

	// Parse existing dest keys.
	existingKeys := parseEnvKeys(destData)

	// Build lines to append: src keys not already in dest.
	var additions []string
	scanner := bufio.NewScanner(strings.NewReader(string(srcData)))
	for scanner.Scan() {
		line := scanner.Text()
		key := envLineKey(line)
		if key == "" {
			// Comment or blank — skip (do not copy comments from template to existing file).
			continue
		}
		if !existingKeys[key] {
			additions = append(additions, line)
		}
	}

	if len(additions) == 0 {
		return nil
	}

	// Append new keys to dest, preceded by a blank line separator if the
	// dest does not already end with a newline.
	f, err := os.OpenFile(dest, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open dest for append: %w", err)
	}

	var writeErr error
	// Ensure a trailing newline before appending.
	if len(destData) > 0 && destData[len(destData)-1] != '\n' {
		_, writeErr = f.WriteString("\n")
	}
	for _, line := range additions {
		if writeErr != nil {
			break
		}
		_, writeErr = f.WriteString(line + "\n")
	}

	if closeErr := f.Close(); closeErr != nil && writeErr == nil {
		return closeErr
	}
	return writeErr
}

// parseEnvKeys returns a set of KEY names found in an .env file content.
func parseEnvKeys(data []byte) map[string]bool {
	keys := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if key := envLineKey(scanner.Text()); key != "" {
			keys[key] = true
		}
	}
	return keys
}

// envLineKey returns the KEY part of a "KEY=VALUE" env line.
// Returns "" for blank lines and comment lines (starting with #).
func envLineKey(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	key, _, _ := strings.Cut(line, "=")
	return strings.TrimSpace(key)
}
