package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/lock"
	"devbox-cli/internal/notify"
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/userconfig"

	"github.com/spf13/cobra"
)

func newDeployCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy pipeline commands",
		Long: `Run and inspect the declarative deploy pipeline defined in devbox/deploy.yml.

The deploy pipeline consists of phases and steps that install, configure, and migrate
application services. Use 'devbox deploy plan' to preview before running.`,
		Example: `  devbox deploy plan
  devbox deploy run
  devbox deploy step init/render-env`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newDeployPlanCmd(flags))
	cmd.AddCommand(newDeployRunCmd(flags))
	cmd.AddCommand(newDeployStepCmd(flags))
	cmd.AddCommand(newDeployStateCmd(flags))
	return cmd
}

func newDeployPlanCmd(flags *rootFlags) *cobra.Command {
	var format string
	var serviceName string

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show resolved deploy plan",
		Long: `Print all phases and steps from devbox/deploy.yml as they would be executed.

The implicit .env generation step is always shown first. Use --service to filter
the plan to steps relevant to a specific service. Use --format shell for script-friendly output.`,
		Example: `  devbox deploy plan
  devbox deploy plan --service main
  devbox deploy plan --format shell`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}

			var steps []pipeline.ResolvedStep
			if serviceName != "" {
				if _, ok := cfg.Services[serviceName]; !ok {
					return fmt.Errorf("service %q not found in config", serviceName)
				}
				steps, err = deploy.ResolveServicePlan(cfg, reg, serviceName)
			} else {
				steps, err = deploy.ResolvePlan(cfg, reg)
			}
			if err != nil {
				return fmt.Errorf("resolving deploy plan: %w", err)
			}

			devboxBin := config.DevboxBin(cfg)
			switch format {
			case "shell":
				deploy.PrintPlanShell(steps, cmd.OutOrStdout(), devboxBin)
			default:
				pipeline.PrintPlanTable(steps, render.Stdout(), devboxBin)
			}
			return nil
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or shell")
	cmd.Flags().StringVar(&serviceName, "service", "", "show plan for a single service only")
	return cmd
}

// newDeployRunCmd creates the `devbox deploy run` command.
// It executes the resolved deploy plan step by step with state tracking,
// idempotency, and optional resumption from prior failed runs.
//
// File logging is controlled by the top-level `log:` field in devbox/deploy.yml
// (default: enabled). When enabled, devbox status messages are teed to
// .devbox/logs/deploy.log; child process output (docker, make) goes directly to
// os.Stdout/os.Stderr so TTY detection works.
func newDeployRunCmd(flags *rootFlags) *cobra.Command {
	var serviceName string
	var force bool
	var resume bool
	var nonInteractive bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the deploy plan",
		Long: `Execute the full deploy pipeline from devbox/deploy.yml phase by phase.

Steps are run in declaration order. The .env file is regenerated as the implicit
first step. Use --service to run only the steps relevant to a specific service.

State tracking allows idempotent deploys: steps that previously succeeded with
matching hashes are skipped on re-run. Use --force to ignore prior state and
re-run all steps (when: conditions are still evaluated — for a fully clean
install run 'devbox reset run && devbox deploy run'). Use --resume to continue
from the last failed step in a partially deployed project.

File logging is enabled by default for deploy and writes to .devbox/logs/deploy.log.
Disable it with 'log: false' at the top of devbox/deploy.yml.`,
		Example: `  devbox deploy run
  devbox deploy run --service main
  devbox deploy run --force
  devbox deploy run --resume`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployRunCmd(flags, serviceName, force, resume, nonInteractive)
		},
	}

	cmd.Flags().StringVar(&serviceName, "service", "", "deploy a single service only")
	cmd.Flags().BoolVar(&force, "force", false, "ignore state and re-run all steps (when: still applies; use 'devbox reset run && devbox deploy run' for a true clean install)")
	cmd.Flags().BoolVar(&resume, "resume", false, "continue from the last failed step")
	cmd.Flags().BoolVarP(&nonInteractive, "non-interactive", "y", false, "suppress interactive prompts")
	return cmd
}

// lockHeldError is returned when a deploy or reset lock is held by another process.
// Implements ExitCode() int so main.go translates it to exit code 2.
type lockHeldError struct {
	operation string
	pid       int
}

func (e *lockHeldError) Error() string {
	return fmt.Sprintf("cannot start %s: lock held by process %d (wait for that process to finish or kill it and retry)", e.operation, e.pid)
}

func (e *lockHeldError) ExitCode() int { return 2 }

// deployCancelledError is returned when the user explicitly cancels via the
// interactive dialog. Notification is suppressed — cancellation is intentional,
// not a failure.
type deployCancelledError struct{}

func (e *deployCancelledError) Error() string { return "deploy cancelled" }

func deployRunCmd(flags *rootFlags, serviceName string, force bool, resume bool, nonInteractive bool) (err error) {
	workDir := flags.ProjectRoot()
	stateDir := filepath.Join(workDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")
	lockPath := filepath.Join(stateDir, "deploy.lock")

	// Install notifier defer before any error-returning step so even an
	// early config-load failure produces a "deploy failed" notification.
	// projectName stays empty until main config load succeeds, which is
	// fine — the backend renders just the operation + duration in that
	// case.
	start := time.Now()
	var projectName string
	ucfg, ucfgErr := userconfig.Load(workDir)
	if ucfgErr != nil {
		slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
		ucfg = nil
	}
	n := newNotifier(ucfg)
	defer func() {
		// Lock-held or user-cancelled — neither is a run failure.
		if errors.As(err, new(*lockHeldError)) || errors.As(err, new(*deployCancelledError)) {
			return
		}
		n.Notify(context.Background(), notify.Event{
			Kind:      notify.OpDeploy,
			Operation: "deploy",
			Outcome:   notify.OutcomeFromErr(err),
			Duration:  time.Since(start),
			Err:       err,
			Project:   projectName,
		})
	}()

	// Acquire file lock to prevent parallel deploys
	lck, err := lock.Acquire(lockPath)
	if err != nil {
		if heldErr, ok := errors.AsType[*lock.HeldError](err); ok {
			lhe := &lockHeldError{operation: "deploy", pid: heldErr.PID}
			render.Stdout().Error(lhe.Error())
			return lhe
		}
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer func() {
		_ = lck.Release()
	}()

	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	projectName = cfg.Project.Name

	dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loading docker config: %w", err)
		}
		dockerCfg = &config.DockerConfig{}
	}
	if err := docker.EnsureVolumes(dockerCfg.Resources, dockerCfg.ProjectName, "deploy", config.DockerBin(cfg), render.Stdout()); err != nil {
		return fmt.Errorf("ensuring volumes: %w", err)
	}

	reg, err := loadCommandRegistry(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	var steps []pipeline.ResolvedStep
	if serviceName != "" {
		if _, ok := cfg.Services[serviceName]; !ok {
			return fmt.Errorf("service %q not found in config", serviceName)
		}
		steps, err = deploy.ResolveServicePlan(cfg, reg, serviceName)
	} else {
		steps, err = deploy.ResolvePlan(cfg, reg)
	}
	if err != nil {
		return fmt.Errorf("resolving deploy plan: %w", err)
	}

	logEnabled := cfg.Deploy.LogEnabled()
	w, logWriter, termOut, logPath, cleanup, err := pipeline.OpenPipelineLog(workDir, "deploy", logEnabled)
	if err != nil {
		return err
	}
	defer cleanup()

	rep := pipeline.NewPlainReporter(w, logWriter, termOut)
	defer rep.Close()

	// After .env is regenerated, load it into the current process
	// environment so subsequent cmd: steps can reference its variables.
	postStepHooks := map[string]func() error{
		deploy.ImplicitEnvStep.Name: func() error {
			return deploy.SourceDotEnv(filepath.Join(workDir, ".env"))
		},
	}

	// Load existing state if present
	state, err := journal.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	baseDir := filepath.Dir(flags.configPath)

	// Load project-level deploy config (absent is valid — some projects only have per-service deploy files)
	projectDeploy, err := config.LoadDeployConfig(filepath.Join(baseDir, "devbox", "deploy.yml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("loading project deploy config: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		projectDeploy = nil
	}

	// Load tracked services and their deploy configs
	trackedServices, svcDeploys, err := deploy.LoadTrackedServices(cfg, reg, baseDir)
	if err != nil {
		return fmt.Errorf("loading tracked services: %w", err)
	}

	// Precompute current hashes for all tracked services
	serviceHashes := make(map[string]string)
	for _, name := range trackedServices {
		svcCfg := cfg.Services[name]
		serviceHashes[name] = journal.ServiceConfigHash(svcCfg, svcDeploys[name])
	}

	// For a --service run, also hash the explicitly requested service even if it
	// is not in the tracked set (e.g. it has a per-service deploy file but the
	// top-level deploy.yml has no deploy_services: true phase). Without this,
	// serviceHashes[serviceName] stays "" and the skip decider compares "" to the
	// stored "" on every re-run, so service config changes never invalidate the
	// journal and stale steps get skipped indefinitely.
	if serviceName != "" {
		if _, alreadyHashed := serviceHashes[serviceName]; !alreadyHashed {
			extraDeploys, err := config.LoadServiceDeployConfigs(baseDir, map[string]config.ServiceConfig{
				serviceName: cfg.Services[serviceName],
			})
			if err != nil {
				return fmt.Errorf("loading deploy config for service %q: %w", serviceName, err)
			}
			serviceHashes[serviceName] = journal.ServiceConfigHash(cfg.Services[serviceName], extraDeploys[serviceName])
		}
	}

	projectHash := journal.ProjectConfigHash(cfg, projectDeploy, svcDeploys, trackedServices)

	// Check if we need to prompt before running
	isInteractive := ui.IsInteractiveFn(os.Stdin) && !nonInteractive

	// Set to true when the config-change dialog fires so the prevIncomplete gate
	// doesn't show a second prompt for the same run.
	configChangeHandled := false

	// Compute scope-relevant state. For --service NAME, the gates below apply
	// to that service only; otherwise they apply to the whole project.
	// When --service targets a never-deployed service, all scope* vars stay
	// zero so no gate fires and the deploy proceeds silently.
	var (
		scopeStatus        journal.Status
		scopeConfigHash    string
		scopeExpectedHash  string
		scopeLastRunStatus journal.Status
	)
	if serviceName == "" {
		scopeStatus = state.Project.Status
		scopeConfigHash = state.Project.ConfigHash
		scopeExpectedHash = projectHash
		if state.Project.LastRun != nil {
			scopeLastRunStatus = state.Project.LastRun.Status
		}
	} else if svc, ok := state.Services[serviceName]; ok {
		scopeStatus = svc.Status
		scopeConfigHash = svc.ConfigHash
		scopeExpectedHash = serviceHashes[serviceName]
		if svc.LastRun != nil {
			scopeLastRunStatus = svc.LastRun.Status
		}
	}

	if !force && scopeStatus == journal.StatusDeployed {
		// Check if all hashes match and there are no check: or files_gate: steps.
		// Steps with files_gate must always re-evaluate the gate (journal-skip bypass),
		// so an early return here would violate that policy.
		hasCheckSteps := false
		hasFilesGateSteps := false
		for _, rs := range steps {
			if rs.Step.Check != nil {
				hasCheckSteps = true
			}
			if rs.FilesGate != nil {
				hasFilesGateSteps = true
			}
			if rs.Parallel != nil {
				for _, sub := range rs.Parallel.Steps {
					if sub.Step.Check != nil {
						hasCheckSteps = true
					}
					if sub.FilesGate != nil {
						hasFilesGateSteps = true
					}
				}
			}
			if hasCheckSteps && hasFilesGateSteps {
				break
			}
		}

		// For a project-wide deploy, also verify every tracked service is present
		// and deployed with a matching hash. A prior --service run stamps
		// project.status=deployed for only the services it ran, so without this
		// check a subsequent full deploy would incorrectly skip services that
		// were never deployed. The --service case implicitly checks the one
		// targeted service via scopeConfigHash == scopeExpectedHash below.
		allTrackedDeployed := true
		if serviceName == "" {
			for _, name := range trackedServices {
				svc, ok := state.Services[name]
				if !ok || svc.Status != journal.StatusDeployed || svc.ConfigHash != serviceHashes[name] {
					allTrackedDeployed = false
					break
				}
			}
		}

		lastRunFailed := scopeLastRunStatus == journal.StatusFailed
		if allTrackedDeployed && !hasCheckSteps && !hasFilesGateSteps && scopeConfigHash == scopeExpectedHash && !lastRunFailed {
			// In-scope state matches and is clean — skip the pipeline.
			w.Info("already up-to-date, use `devbox reset && devbox deploy` to redeploy")
			return nil
		}

		// Config hash diverged but state is deployed
		if scopeConfigHash != scopeExpectedHash {
			if isInteractive {
				// Prompt for action
				w.Tip("Tip: 'when:' conditions are always re-evaluated. For a fully clean install (drop service dirs, volumes, etc.) cancel and run 'devbox reset run && devbox deploy run'.")
				choice, err := ui.RunSelector(
					"Deployed config changed. Choose action:",
					[]ui.SelectorItem{
						{Label: "Apply changes (re-run only changed steps)"},
						{Label: "Re-run all steps (ignore state; when: still applies)"},
						{Label: "Cancel"},
					},
				)
				if err != nil {
					return err
				}
				if choice == 2 {
					return &deployCancelledError{}
				}
				if choice == 1 {
					force = true // Full re-deploy
				}
				// choice == 0: apply delta (default behavior, continue)
				// Mark as handled so we don't show the prevIncomplete prompt too —
				// the user already acknowledged the state.
				configChangeHandled = true
			}
		}
	}

	// Check for previously failed/partial/crashed runs in the active scope.
	// For project-wide: also covers the case where all services deployed but a
	// project-scope step failed (Recompute sets project.status=deployed, driven
	// by service statuses, but last_run.status=failed). For --service NAME:
	// only the targeted service's state is consulted, so an unrelated failed
	// project run does not block deploying a brand-new service.
	prevIncomplete := scopeStatus == journal.StatusFailed ||
		scopeStatus == journal.StatusPartial ||
		scopeLastRunStatus == journal.StatusInProgress ||
		scopeLastRunStatus == journal.StatusFailed
	if !force && prevIncomplete && !configChangeHandled {
		if isInteractive {
			w.Warning("Last deploy run failed or was incomplete.")
			w.Tip("Tip: 'when:' conditions are always re-evaluated, so partially-installed services may stay skipped. For a fully clean install (drop service dirs, volumes, etc.) cancel and run 'devbox reset run && devbox deploy run'.")
			choice, err := ui.RunSelector(
				"Failed deploy detected:",
				[]ui.SelectorItem{
					{Label: "Resume (skip steps already done)"},
					{Label: "Re-run all steps (ignore state; when: still applies)"},
					{Label: "Cancel"},
				},
			)
			if err != nil {
				return err
			}
			if choice == 2 {
				return errors.New("deploy cancelled")
			}
			if choice == 1 {
				force = true // Re-run all steps (state ignored; when: still applies)
			}
			// choice == 0: resume (default)
		} else if !resume {
			// Non-interactive mode: error unless --resume or --force
			return fmt.Errorf("last deploy failed or was incomplete; use --resume to continue, --force to re-run all steps (when: still applies), or run 'devbox reset run && devbox deploy run' for a fully clean install")
		}
	}

	// When forcing a full re-run, start with a clean state so the FileRecorder
	// doesn't inherit stale entries from a previous run.
	if force {
		state = &journal.ProjectState{SchemaVersion: "1"}
	}

	// Clear stale phase data for any scope whose config hash has changed.
	// The skip decider correctly forces those steps to Run, but the old Steps
	// map entries remain in the recorder's in-memory state. phaseStatusFromSteps
	// iterates all steps in the phase, so a renamed or removed step from the
	// previous config (still present as StatusFailed) would cause the phase to
	// report failed even when every current step succeeds.
	if !force {
		if state.Project != nil && state.Project.ConfigHash != projectHash {
			state.Project.Phases = make(map[string]*journal.PhaseState)
		}
		if state.Services != nil {
			for svcName, svcState := range state.Services {
				if svcState.ConfigHash != serviceHashes[svcName] {
					svcState.Phases = make(map[string]*journal.PhaseState)
				}
			}
		}
	}

	// Build the skip decider closure
	skipDecider := func(addr string, rs pipeline.ResolvedStep, actionHash string) journal.Decision {
		if force {
			return journal.Run
		}

		// Determine scope and check config hash
		var prevStep *journal.StepState

		if rs.Service == "" {
			// Project-scope step
			if state.Project.ConfigHash != projectHash {
				// Project config changed; treat as absent
				return journal.Run
			}
			if state.Project.Phases != nil {
				if phase, ok := state.Project.Phases[rs.Phase.Name]; ok {
					if step, ok := phase.Steps[rs.Step.Name]; ok {
						prevStep = step
					}
				}
			}
		} else if state.Services != nil {
			// Service-scope step
			if svcState, ok := state.Services[rs.Service]; ok {
				if svcState.ConfigHash != serviceHashes[rs.Service] {
					// Service config changed; treat as absent
					return journal.Run
				}
				if svcState.Phases != nil {
					if phase, ok := svcState.Phases[rs.Phase.Name]; ok {
						if step, ok := phase.Steps[rs.Step.Name]; ok {
							prevStep = step
						}
					}
				}
			}
		}

		hasCheck := rs.Step.Check != nil
		return journal.Decide(prevStep, actionHash, hasCheck)
	}

	// Construct the FileRecorder. Only stamp project hash for full deploys;
	// a --service run includes the implicit env step (project-scoped) but must
	// not advance the project hash since the actual project steps did not run.
	recorder := pipeline.NewFileRecorder(statePath, state, serviceHashes, projectHash, serviceName == "")

	// Run the pipeline with state tracking
	opts := pipeline.RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "deploy",
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		WorkDir:      workDir,
		LogWriter:    logWriter,
		SkipConfirm:  nonInteractive,
		PostStepHook: postStepHooks,
		Recorder:     recorder,
		SkipDecider:  recorder.WrapSkipDecider(skipDecider),
	}

	if pipeErr := pipeline.RunWithOptions(opts); pipeErr != nil {
		if errors.Is(pipeErr, ErrSilent) && logEnabled {
			w.Warning("Full output saved to: " + logPath)
		}
		if recErr := recorder.Err(); recErr != nil {
			if errors.Is(pipeErr, ErrSilent) {
				// ErrSilent suppresses all error output; warn explicitly so
				// the state-save failure is not silently swallowed.
				w.Warning("deploy state could not be saved: " + recErr.Error())
				return pipeErr
			}
			return errors.Join(pipeErr, fmt.Errorf("deploy state could not be saved: %w", recErr))
		}
		return pipeErr
	}

	// Surface any state-persistence failures even when all steps succeeded.
	// If flush failed, idempotency guarantees are broken for the next run.
	if err := recorder.Err(); err != nil {
		return fmt.Errorf("deploy completed but state could not be saved: %w", err)
	}

	if logEnabled {
		w.Info("Deploy log saved to: " + logPath)
	}
	return nil
}

func newDeployStepCmd(flags *rootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "step <phase>/<step>",
		Short: "Run a single deploy step by <phase>/<step> address",
		Long: `Execute a single step from the deploy pipeline by its address.

The address format is '<phase>/<step>' (e.g. 'init/render-env') or '<service>/<phase>/<step>'.
Use 'devbox deploy plan' to list available step addresses. Use --dry-run to preview without executing.`,
		Example: `  devbox deploy step init/render-env
  devbox deploy step main/setup/migrate
  devbox deploy step init/render-env --dry-run`,
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var completions []string
			completions = cobra.AppendActiveHelp(completions, "Use 'devbox deploy plan' to see available phase/step addresses")
			configPath, _, err := completionConfigPath(flags, cmd)
			if err != nil {
				return completions, cobra.ShellCompDirectiveNoFileComp
			}
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return completions, cobra.ShellCompDirectiveNoFileComp
			}
			reg, err := loadCommandRegistry(configPath)
			if err != nil {
				return completions, cobra.ShellCompDirectiveNoFileComp
			}
			steps, err := deploy.ResolvePlan(cfg, reg)
			if err != nil {
				return completions, cobra.ShellCompDirectiveNoFileComp
			}
			for _, s := range steps {
				if s.Parallel != nil {
					// Parallel groups cannot be run individually; skip from completions.
					continue
				}
				addr := s.StepAddress()
				desc := s.Step.Description
				if desc == "" {
					desc = s.Step.Name
				}
				completions = append(completions, cobra.CompletionWithDesc(addr, desc))
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			address := args[0]
			phase, step, err := deploy.FindStep(cfg, address)
			if err != nil {
				return err
			}

			if step.Parallel != nil {
				return fmt.Errorf("step %q is a parallel group and cannot be run individually; use 'devbox deploy run' to execute the full pipeline", address)
			}

			// Evaluate when condition.
			if step.When != nil {
				var (
					ok  bool
					err error
				)
				if step.When.IsRuntime() {
					ok, err = condition.EvalRuntimeTyped(step.When, flags.ProjectRoot())
				} else if step.When.Type == condition.TypeTemplate {
					ok, err = tpl.EvalCondition(step.When.Expr, cfg)
				}
				if err != nil {
					return fmt.Errorf("evaluating when condition for %s: %w", address, err)
				}
				if !ok {
					render.Stdout().Warning(fmt.Sprintf("skipping step %s/%s: when condition is false", phase.Name, step.Name))
					return nil
				}
			}

			if step.FilesGate != nil {
				render.Stdout().Warning(fmt.Sprintf("note: files_gate on step %s/%s is not evaluated by this command", phase.Name, step.Name))
			}

			resolved := pipeline.StepCommand(step, config.DevboxBin(cfg))
			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), resolved)
				return nil
			}

			workDir := flags.ProjectRoot()
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}
			actx := pipeline.ActionContext{
				WorkDir:     workDir,
				Cfg:         cfg,
				Reg:         reg,
				LogWriter:   nil,
				SkipConfirm: false,
			}

			if err := pipeline.ExecAction(cmd.Context(), step.Action(), actx); err != nil {
				return err
			}

			if step.Check != nil {
				if err := pipeline.ExecAction(cmd.Context(), *step.Check, actx); err != nil {
					return fmt.Errorf("step %s: check failed: %w", address, err)
				}
			}

			return nil
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved command without executing")
	return cmd
}
