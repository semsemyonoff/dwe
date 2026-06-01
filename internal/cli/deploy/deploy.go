package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	pipeline "github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/execution/preflight"
	"github.com/semsemyonoff/dwe/internal/core/notify"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// NewCmd builds the `dwe deploy` command tree (plan / run / state) and
// the interactive menu shown when invoked with no subcommand in a TTY.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "deploy",
		Short:   "Deploy pipeline commands",
		Long: `Run and inspect the declarative deploy pipeline defined in workspace/deploy.yml.

The deploy pipeline consists of phases and steps that install, configure, and migrate
application services. Use 'dwe deploy plan' to preview before running.`,
		Example: `  dwe deploy plan
  dwe deploy run`,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployMenu(cmd, flags)
		},
	}
	cmd.AddCommand(newDeployPlanCmd(flags))
	cmd.AddCommand(newDeployRunCmd(flags))
	cmd.AddCommand(newDeployStateCmd(flags))
	return cmd
}

// deployPlanOpts holds the options for runDeployPlan.
type deployPlanOpts struct {
	ServiceName string
	Format      string
}

// runDeployPlan is the common implementation for `dwe deploy plan` and menu dispatch.
func runDeployPlan(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts deployPlanOpts) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Apply deploy default when no deploy.yml is on disk (cfg.Deploy has empty
	// phases in that case, set by LoadConfig). The notice is suppressed when
	// rendering a per-service plan — the orchestrator default is not driving
	// the output in that case (ResolveServicePlan reads the per-service file).
	ensuredDeploy, deployDefaulted := deploy.EnsureDeployConfig(cfg.Deploy)
	cfg.Deploy = ensuredDeploy
	if deployDefaulted && opts.ServiceName == "" {
		cmdctx.EmitDefaultNotice(cmd, flags, "deploy", "deploy")
	}

	reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}
	// Apply hide: visibility so the pipeline executor and workflow runner
	// see Hidden=true on user commands gated by the active config.
	// Fail-open — per-expression eval failures are logged and treated as
	// visible.
	_ = reg.ApplyVisibility(cfg, flags.Root)

	var steps []pipeline.ResolvedStep
	if opts.ServiceName != "" {
		if _, ok := cfg.Services[opts.ServiceName]; !ok {
			return fmt.Errorf("service %q not found in config", opts.ServiceName)
		}
		steps, err = deploy.ResolveServicePlan(cfg, reg, opts.ServiceName)
	} else {
		steps, err = deploy.ResolvePlan(cfg, reg)
	}
	if err != nil {
		return fmt.Errorf("resolving deploy plan: %w", err)
	}

	dweBin := config.DweBin(cfg)
	switch opts.Format {
	case "shell":
		if opts.ServiceName != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "# Deploy plan for service %s\n", opts.ServiceName)
		} else {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "# Deploy plan")
		}
		deploy.PrintPlanShell(steps, cmd.OutOrStdout(), dweBin)
	default:
		title := "Deploy plan"
		if opts.ServiceName != "" {
			title = fmt.Sprintf("Deploy plan for service %s", opts.ServiceName)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), render.SectionTitle(title))
		pipeline.PrintPlanTable(steps, sharedrender.NewWriter(cmd.OutOrStdout()), dweBin)
	}
	return nil
}

func newDeployPlanCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var format string
	var serviceName string

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show resolved deploy plan",
		Long: `Print all phases and steps from workspace/deploy.yml as they would be executed.

The implicit .env generation step is always shown first. Use --service to filter
the plan to steps relevant to a specific service. Use --format shell for script-friendly output.`,
		Example: `  dwe deploy plan
  dwe deploy plan --service main
  dwe deploy plan --format shell`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := deployPlanOpts{
				ServiceName: serviceName,
				Format:      format,
			}
			return runDeployPlan(cmd.Context(), cmd, flags, opts)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or shell")
	cmd.Flags().StringVar(&serviceName, "service", "", "show plan for a single service only")
	return cmd
}

// newDeployRunCmd creates the `dwe deploy run` command.
// It executes the resolved deploy plan step by step with state tracking,
// idempotency, and optional resumption from prior failed runs.
//
// File logging is controlled by the top-level `log:` field in workspace/deploy.yml
// (default: enabled). When enabled, dwe status messages are teed to
// .dwe/logs/deploy.log; child process output (docker, make) goes directly to
// os.Stdout/os.Stderr so TTY detection works.
func newDeployRunCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var serviceName string
	var force bool
	var resume bool
	var nonInteractive bool
	var skipPreflight bool
	var silent bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the deploy plan",
		Long: `Execute the full deploy pipeline from workspace/deploy.yml phase by phase.

Steps are run in declaration order. The .env file is regenerated as the implicit
first step. Use --service to run only the steps relevant to a specific service.

State tracking allows idempotent deploys: steps that previously succeeded with
matching hashes are skipped on re-run. Use --force to ignore prior state and
re-run all steps (when: conditions are still evaluated — for a fully clean
install run 'dwe reset run && dwe deploy run'). Use --resume to continue
from the last failed step in a partially deployed project.

File logging is enabled by default for deploy and writes to .dwe/logs/deploy.log.
Disable it with 'log: false' at the top of workspace/deploy.yml.`,
		Example: `  dwe deploy run
  dwe deploy run --service main
  dwe deploy run --force
  dwe deploy run --resume`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployRunCmd(cmd, flags, serviceName, force, resume, nonInteractive, skipPreflight, silent)
		},
	}

	cmd.Flags().StringVar(&serviceName, "service", "", "deploy a single service only")
	cmd.Flags().BoolVar(&force, "force", false, "ignore state and re-run all steps (when: still applies; use 'dwe reset run && dwe deploy run' for a true clean install)")
	cmd.Flags().BoolVar(&resume, "resume", false, "continue from the last failed step")
	cmd.Flags().BoolVarP(&nonInteractive, "non-interactive", "y", false, "suppress interactive prompts")
	cmdctx.AddSkipPreflight(cmd, &skipPreflight)
	cmdctx.AddSilent(cmd, &silent)
	return cmd
}

// deployCancelledError is returned when the user explicitly cancels via the
// interactive dialog. Notification is suppressed — cancellation is intentional,
// not a failure. ExitCode returns 0 so fang suppresses the "Error:" line.
type deployCancelledError struct{}

func (e *deployCancelledError) Error() string { return "deploy cancelled" }
func (e *deployCancelledError) ExitCode() int { return 0 }

// deployMissingDepsConfirmFn is the swap seam for the after-deps confirmation
// form. Tests override this to inject Yes/No/cancelled without driving stdin.
var deployMissingDepsConfirmFn = widgets.RunConfirm

// confirmMissingDeps handles the "declared after: deps not in this run" gate.
// Interactive: prompts via deployMissingDepsConfirmFn; cancellation (Esc or
// Cancel button) returns *deployCancelledError. Non-interactive: logs to
// stderr and proceeds.
func confirmMissingDeps(cmd *cobra.Command, services, missing []string, isInteractive bool) error {
	if !isInteractive {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"info: services %v declare after: deps not in this run; missing deploys: %v\n",
			services, missing)
		return nil
	}
	title := fmt.Sprintf("Declared after: deps not in this run — proceed anyway? (missing: %s)",
		strings.Join(missing, ", "))
	ok, err := deployMissingDepsConfirmFn(title, "Proceed", "Cancel")
	if err != nil {
		if errors.Is(err, widgets.ErrCancelled) {
			return &deployCancelledError{}
		}
		return err
	}
	if !ok {
		return &deployCancelledError{}
	}
	return nil
}

// Opts holds the options for runDeployHelper.
type Opts struct {
	Services       []string // nil/empty = full project; one = single service; many = batch
	Force          bool
	Resume         bool
	NonInteractive bool
	SkipPreflight  bool
	// Silent suppresses the end-of-run desktop notification.
	Silent bool
	// SuppressPendingClear prevents runDeployHelper from clearing pending-deploy
	// journal entries after a successful run. Set by the toggle executor, which
	// owns the pending clear itself via a single atomic ClearPendingOps call
	// after ALL apply steps succeed — so the inner clear must not fire on a
	// per-step basis.
	SuppressPendingClear bool
	// PreflightFn overrides the preflight implementation. Defaults to
	// preflight.Run when nil. Set by tests to bypass real env probes;
	// production callers leave it zero.
	PreflightFn preflight.RunFn
}

// deployRunOpts holds the options for runDeployRun.
type deployRunOpts struct {
	ServiceName    string
	Force          bool
	Resume         bool
	NonInteractive bool
	SkipPreflight  bool
	Silent         bool
}

// runDeployRun is the common implementation for `dwe deploy run` and menu dispatch.
func runDeployRun(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts deployRunOpts) error {
	var services []string
	if opts.ServiceName != "" {
		services = []string{opts.ServiceName}
	}
	return RunHelper(ctx, cmd, flags, Opts{
		Services:       services,
		Force:          opts.Force,
		Resume:         opts.Resume,
		NonInteractive: opts.NonInteractive,
		SkipPreflight:  opts.SkipPreflight,
		Silent:         opts.Silent,
	})
}

func deployRunCmd(cmd *cobra.Command, flags *cmdctx.RootFlags, serviceName string, force bool, resume bool, nonInteractive bool, skipPreflight bool, silent bool) error {
	return runDeployRun(cmd.Context(), cmd, flags, deployRunOpts{
		ServiceName:    serviceName,
		Force:          force,
		Resume:         resume,
		NonInteractive: nonInteractive,
		SkipPreflight:  skipPreflight,
		Silent:         silent,
	})
}

// RunHelper executes the deploy pipeline. It is exported so the service
// toggle executor can drive deploys with the same orchestration as the
// `dwe deploy run` cobra command.
func RunHelper(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts Opts) (err error) {
	workDir := flags.ProjectRoot()
	stateDir := filepath.Join(workDir, ".dwe", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	// Install notifier defer before any error-returning step so even an
	// early config-load failure produces a "deploy failed" notification.
	// projectName stays empty until main config load succeeds, which is
	// fine — the backend renders just the operation + duration in that
	// case.
	start := time.Now()
	var projectName string
	var isNoop bool
	if !opts.Silent {
		ucfg, ucfgErr := userpkg.Load(workDir)
		if ucfgErr != nil {
			slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
			ucfg = nil
		}
		n := newNotifier(ucfg)
		defer func() {
			// Lock-held, preflight-blocked, user-cancelled, or already-up-to-date
			// — none is a deploy *run* failure; suppress the notification.
			if errors.As(err, new(*lock.ProjectLockHeldError)) || errors.As(err, new(*preflight.Error)) || errors.As(err, new(*deployCancelledError)) || isNoop {
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
	}

	// Load cfg + registry BEFORE acquiring the deploy lock so preflight can
	// reject without leaving a stale lock file in .dwe/deploy/.
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	projectName = cfg.Project.Name

	reg, regErr := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if regErr != nil {
		// nil-tolerant during preflight; surface the real error after.
		reg = nil
	}
	if reg != nil {
		_ = reg.ApplyVisibility(cfg, flags.Root)
	}

	runPreflight := opts.PreflightFn
	if runPreflight == nil {
		runPreflight = preflight.Run
	}
	if err := runPreflight(ctx, cfg, reg, workDir, "deploy", opts.SkipPreflight, cmd.ErrOrStderr()); err != nil {
		return err
	}
	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	// Acquire deploy + snapshot project locks to prevent parallel deploys
	// and to be mutually exclusive with snapshot mutating operations.
	releaseLocks, err := lock.AcquireProjectLocks(workDir)
	if err != nil {
		if phe, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
			sharedrender.Stdout().Error(phe.Error())
			return phe
		}
		return fmt.Errorf("acquiring project locks: %w", err)
	}
	defer releaseLocks()

	dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loading docker config: %w", err)
		}
		dockerCfg = &config.DockerConfig{}
	}
	if err := docker.EnsureVolumes(dockerCfg.Resources, dockerCfg.ProjectName, "deploy", config.DockerBin(cfg), sharedrender.Stdout()); err != nil {
		return fmt.Errorf("ensuring volumes: %w", err)
	}

	baseDir := filepath.Dir(flags.ConfigPath)

	// Load project-level deploy config (absent is valid — some projects only have per-service deploy files)
	projectDeploy, err := config.LoadProjectDeployConfig(filepath.Join(baseDir, "workspace", "deploy.yml"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		projectDeploy = nil
	case err != nil:
		return fmt.Errorf("load deploy config: %w", err)
	}
	projectDeploy, deployDefaulted := deploy.EnsureDeployConfig(projectDeploy)
	// Reconcile: downstream resolvers in workflow/deploy read cfg.Deploy.Phases directly.
	// Overwrite here so the default propagates through ResolvePlan.
	cfg.Deploy = projectDeploy
	// Per-service runs (ResolveServicePlan / ResolveServicesPlanSubset) do not read
	// cfg.Deploy.Phases, so the orchestrator-default notice is only meaningful for
	// whole-project runs.
	if deployDefaulted && len(opts.Services) == 0 {
		cmdctx.EmitDefaultNotice(cmd, flags, "deploy", "deploy")
	}

	// Load tracked services and their deploy configs
	trackedServices, svcDeploys, err := deploy.LoadTrackedServices(cfg, reg, baseDir)
	if err != nil {
		return fmt.Errorf("loading tracked services: %w", err)
	}

	// For subset deploys: also load deploy configs for requested services not in svcDeploys
	if len(opts.Services) > 0 {
		toLoad := make(map[string]config.ServiceConfig)
		for _, name := range opts.Services {
			svcCfg, ok := cfg.Services[name]
			if !ok {
				return fmt.Errorf("service %q not found in config", name)
			}
			if _, already := svcDeploys[name]; !already {
				toLoad[name] = svcCfg
			}
		}
		if len(toLoad) > 0 {
			extra, err := config.LoadServiceDeployConfigs(baseDir, toLoad)
			if err != nil {
				return fmt.Errorf("loading deploy configs for requested services: %w", err)
			}
			maps.Copy(svcDeploys, extra)
		}
	}

	// Precompute current hashes for all tracked services
	serviceHashes := make(map[string]string)
	for _, name := range trackedServices {
		svcCfg := cfg.Services[name]
		serviceHashes[name] = journal.ServiceConfigHash(svcCfg, svcDeploys[name])
	}
	// Also hash requested services not in the tracked set
	for _, name := range opts.Services {
		if _, ok := serviceHashes[name]; !ok {
			serviceHashes[name] = journal.ServiceConfigHash(cfg.Services[name], svcDeploys[name])
		}
	}

	// Resolve the deploy plan
	var steps []pipeline.ResolvedStep
	switch {
	case len(opts.Services) == 0:
		steps, err = deploy.ResolvePlan(cfg, reg)
	case len(opts.Services) == 1:
		steps, err = deploy.ResolveServicePlan(cfg, reg, opts.Services[0])
	default:
		subsetDeploys := make(map[string]*config.ServiceDeployConfig, len(opts.Services))
		for _, name := range opts.Services {
			subsetDeploys[name] = svcDeploys[name]
		}
		steps, err = deploy.ResolveServicesPlanSubset(cfg, reg, subsetDeploys, opts.Services)
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

	projectHash := journal.ProjectConfigHash(cfg, projectDeploy, svcDeploys, trackedServices)

	// Check if we need to prompt before running
	isInteractive := widgets.IsInteractiveFn(os.Stdin) && !opts.NonInteractive

	// Set to true when the config-change dialog fires so the prevIncomplete gate
	// doesn't show a second prompt for the same run.
	configChangeHandled := false

	// Compute scope-relevant state. For single-service runs, the gates below apply
	// to that service only; for multi-service, aggregated; otherwise whole project.
	// When targeting a never-deployed service, all scope* vars stay zero so no gate
	// fires and the deploy proceeds silently.
	var (
		scopeStatus        journal.Status
		scopeAllHashMatch  bool
		scopeLastRunStatus journal.Status
	)
	switch {
	case len(opts.Services) == 0:
		scopeStatus = state.Project.Status
		scopeAllHashMatch = state.Project.ConfigHash == projectHash
		if state.Project.LastRun != nil {
			scopeLastRunStatus = state.Project.LastRun.Status
		}
	case len(opts.Services) == 1:
		name := opts.Services[0]
		if svc, ok := state.Services[name]; ok {
			scopeStatus = svc.Status
			scopeAllHashMatch = svc.ConfigHash == serviceHashes[name]
			if svc.LastRun != nil {
				scopeLastRunStatus = svc.LastRun.Status
			}
		}
	default:
		// Multi-service: aggregate across all targeted services.
		allDeployed := true
		allHashMatch := true
		for _, name := range opts.Services {
			svc, ok := state.Services[name]
			if !ok {
				allDeployed = false
				allHashMatch = false
				continue
			}
			switch svc.Status {
			case journal.StatusFailed:
				scopeStatus = journal.StatusFailed
				allDeployed = false
			case journal.StatusPartial:
				if scopeStatus != journal.StatusFailed {
					scopeStatus = journal.StatusPartial
				}
				allDeployed = false
			case journal.StatusDeployed:
				if scopeStatus == "" {
					scopeStatus = journal.StatusDeployed
				}
			default:
				allDeployed = false
			}
			if svc.ConfigHash != serviceHashes[name] {
				allHashMatch = false
			}
			if svc.LastRun != nil {
				switch svc.LastRun.Status {
				case journal.StatusFailed:
					scopeLastRunStatus = journal.StatusFailed
				case journal.StatusInProgress:
					if scopeLastRunStatus != journal.StatusFailed {
						scopeLastRunStatus = journal.StatusInProgress
					}
				}
			}
		}
		if allDeployed {
			scopeStatus = journal.StatusDeployed
		}
		scopeAllHashMatch = allHashMatch
	}

	if !opts.Force && scopeStatus == journal.StatusDeployed {
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
		// were never deployed. The --service/--services cases check only the
		// targeted services via scopeAllHashMatch.
		allTrackedDeployed := true
		if len(opts.Services) == 0 {
			for _, name := range trackedServices {
				svc, ok := state.Services[name]
				if !ok || svc.Status != journal.StatusDeployed || svc.ConfigHash != serviceHashes[name] {
					allTrackedDeployed = false
					break
				}
			}
		}

		lastRunFailed := scopeLastRunStatus == journal.StatusFailed
		if allTrackedDeployed && !hasCheckSteps && !hasFilesGateSteps && scopeAllHashMatch && !lastRunFailed {
			// In-scope state matches and is clean — skip the pipeline.
			isNoop = true
			w.Info("already up-to-date, use `dwe reset && dwe deploy` to redeploy")
			return nil
		}

		// Config hash diverged but state is deployed
		if !scopeAllHashMatch {
			if isInteractive {
				// Prompt for action
				render.PrintSelectorHeader(w.Writer(), projectName, "Deploy")
				w.Tip("Tip: 'when:' conditions are always re-evaluated. For a fully clean install (drop service dirs, volumes, etc.) cancel and run 'dwe reset run && dwe deploy run'.")
				choice, err := widgets.RunSelector(
					"Deployed config changed. Choose action:",
					[]widgets.SelectorItem{
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
					opts.Force = true // Full re-deploy
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
	if !opts.Force && prevIncomplete && !configChangeHandled {
		if isInteractive {
			render.PrintSelectorHeader(w.Writer(), projectName, "Deploy")
			w.Warning("Last deploy run failed or was incomplete.")
			w.Tip("Tip: 'when:' conditions are always re-evaluated, so partially-installed services may stay skipped. For a fully clean install (drop service dirs, volumes, etc.) cancel and run 'dwe reset run && dwe deploy run'.")
			choice, err := widgets.RunSelector(
				"Failed deploy detected:",
				[]widgets.SelectorItem{
					{Label: "Resume (skip steps already done)"},
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
				opts.Force = true // Re-run all steps (state ignored; when: still applies)
			}
			// choice == 0: resume (default)
		} else if !opts.Resume {
			// Non-interactive mode: error unless --resume or --force
			return fmt.Errorf("last deploy failed or was incomplete; use --resume to continue, --force to re-run all steps (when: still applies), or run 'dwe reset run && dwe deploy run' for a fully clean install")
		}
	}

	// Journal-check: warn if after: deps of requested services are not deployed.
	if len(opts.Services) > 0 && !opts.Force {
		if missing := collectMissingDeps(opts.Services, svcDeploys, state); len(missing) > 0 {
			if err := confirmMissingDeps(cmd, opts.Services, missing, isInteractive); err != nil {
				return err
			}
		}
	}

	// When forcing a full re-run, start with a clean state so the FileRecorder
	// doesn't inherit stale entries from a previous run.
	if opts.Force {
		state = &journal.ProjectState{SchemaVersion: "1"}
	}

	// Clear stale phase data for any scope whose config hash has changed.
	// The skip decider correctly forces those steps to Run, but the old Steps
	// map entries remain in the recorder's in-memory state. phaseStatusFromSteps
	// iterates all steps in the phase, so a renamed or removed step from the
	// previous config (still present as StatusFailed) would cause the phase to
	// report failed even when every current step succeeds.
	if !opts.Force {
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
		if opts.Force {
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
	// a --service/--services run includes the implicit env step (project-scoped)
	// but must not advance the project hash since the actual project steps did not run.
	recorder := pipeline.NewFileRecorder(statePath, state, serviceHashes, projectHash, len(opts.Services) == 0)

	// Run the pipeline with state tracking
	runOpts := pipeline.RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "deploy",
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		WorkDir:      workDir,
		LogWriter:    logWriter,
		SkipConfirm:  opts.NonInteractive,
		PostStepHook: postStepHooks,
		Recorder:     recorder,
		SkipDecider:  recorder.WrapSkipDecider(skipDecider),
		Translator:   flags.I18n,
		Locale:       flags.Locale,
	}

	if pipeErr := pipeline.RunWithOptions(runOpts); pipeErr != nil {
		if errors.Is(pipeErr, pipeline.ErrSilent) && logEnabled {
			w.Warning("Full output saved to: " + logPath)
		}
		if recErr := recorder.Err(); recErr != nil {
			if errors.Is(pipeErr, pipeline.ErrSilent) {
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

	clearDeployedPending(statePath, opts, steps)

	if logEnabled {
		w.Info("Deploy log saved to: " + logPath)
	}
	return nil
}

// clearDeployedPending clears pending deploy entries for the services actually
// executed in this run. For full deploys (no opts.Services), only the service
// names present in steps are cleared — full deploy only processes enabled
// services, so disabled-service pending entries must not be discarded here.
// For subset deploys, only the targeted services are cleared.
// No-op when SuppressPendingClear is set (toggle executor owns the clear).
func clearDeployedPending(statePath string, opts Opts, steps []pipeline.ResolvedStep) {
	if opts.SuppressPendingClear {
		return
	}
	if len(opts.Services) == 0 {
		seen := make(map[string]bool)
		var svcs []string
		for _, rs := range steps {
			if rs.Service != "" && !seen[rs.Service] {
				seen[rs.Service] = true
				svcs = append(svcs, rs.Service)
			}
		}
		if len(svcs) == 0 {
			return
		}
		sort.Strings(svcs)
		if clearErr := journal.ClearPendingForServices(statePath, journal.PendingDeploy, svcs); clearErr != nil {
			slog.Warn("clearing pending deploy state after success", "err", clearErr)
		}
	} else {
		if clearErr := journal.ClearPendingForServices(statePath, journal.PendingDeploy, opts.Services); clearErr != nil {
			slog.Warn("clearing pending deploy state after success", "err", clearErr)
		}
	}
}

// collectMissingDeps returns sorted names of after: dependencies of the given
// services that (a) are not being deployed in this run, (b) have a deploy.yml,
// and (c) have not yet reached StatusDeployed in state.
func collectMissingDeps(services []string, svcDeploys map[string]*config.ServiceDeployConfig, state *journal.ProjectState) []string {
	inSet := make(map[string]bool, len(services))
	for _, s := range services {
		inSet[s] = true
	}
	seen := make(map[string]bool)
	var missing []string
	for _, name := range services {
		dc := svcDeploys[name]
		if dc == nil {
			continue
		}
		for _, dep := range dc.After {
			if inSet[dep] {
				continue // being deployed in this run
			}
			if svcDeploys[dep] == nil {
				continue // no deploy.yml, skip
			}
			if depState, ok := state.Services[dep]; ok && depState.Status == journal.StatusDeployed {
				continue // already deployed
			}
			if !seen[dep] {
				seen[dep] = true
				missing = append(missing, dep)
			}
		}
	}
	sort.Strings(missing)
	return missing
}
