package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	localpkg "devbox-cli/internal/core/project/local"
	"devbox-cli/internal/core/project/services"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/validate"
	valchecks "devbox-cli/internal/core/validate/checks"
	valconfig "devbox-cli/internal/core/validate/config"
	valenv "devbox-cli/internal/core/validate/env"
	valsetup "devbox-cli/internal/core/validate/setup"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/core/workflow/setup"

	"charm.land/bubbles/v2/key"
	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
)

// Test seams (package-level vars that tests can override).
var (
	collectPortConflictsFn  = valenv.CollectPortConflicts
	loadSetupYAMLFn         = setup.LoadSetupYAML
	newHuhAskerFn           = setup.NewHuhAsker
	runWizardFn             = setup.Run
	selectMenuItemFn        = selectMenuItemInteractive
	selectDeployServiceFn   = selectDeployServiceInteractive
	buildDeployItemsFn      = buildDeployServiceItems
	runPreWizardPreflightFn = runPreWizardPreflight
	runDeployRunFn          = runDeployRun
	runDeployPlanFn         = runDeployPlan
)

type menuChoice string

const (
	menuRun         menuChoice = "run"
	menuRunService  menuChoice = "run_service"
	menuPlan        menuChoice = "plan"
	menuPlanService menuChoice = "plan_service"
	menuWizard      menuChoice = "wizard"
	menuExit        menuChoice = "exit"
)

// deployServiceItem describes one service entry in the deploy-service submenu.
type deployServiceItem struct {
	Name       string
	Type       string
	Icon       string
	Mandatory  bool
	Deployed   bool
	DeployedAt time.Time
	Locked     bool // true when a mandatory service is not yet deployed and this is optional
	LockedHint string
}

// runDeployMenu is the entry point for `devbox deploy` without subcommands.
// It opens an interactive TUI menu if stdin/stdout are TTY, otherwise prints help.
func runDeployMenu(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	if !ui.IsInteractiveFn(os.Stdin) {
		_ = cmd.Help()
		return usageError("devbox deploy: requires a subcommand or interactive TTY")
	}

	ctx := cmd.Context()
	baseDir := flags.ProjectRoot()
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	setupPath := filepath.Join(baseDir, "devbox", "setup.yml")

	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	existing, err := localpkg.LoadLocalYAML(localPath)
	if err != nil {
		return fmt.Errorf("read existing local.yml: %w", err)
	}

	conflicts, err := collectPortConflictsFn(ctx, cfg, baseDir)
	if err != nil {
		return fmt.Errorf("probing port conflicts: %w", err)
	}

	setupCfg, setupErr := loadSetupYAMLFn(setupPath)

	validators := valsetup.All(setupCfg, setupErr, setupPath)
	registry := validate.NewRegistry()
	for _, v := range validators {
		registry.Register(v)
	}
	vctx := validate.Context{
		Ctx:         ctx,
		ProjectRoot: baseDir,
		ConfigPath:  flags.ConfigPath,
		Cfg:         cfg,
	}
	diags := registry.Run(vctx)

	if len(diags) > 0 {
		rows := ui.FormatDiagnostics(diags, false)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.RenderDiagnosticsTable(rows))

		for _, diag := range diags {
			if diag.Severity >= validate.SeverityError {
				return &deployValidationError{"setup.yml has errors; fix them before deploying"}
			}
		}
	}

	showWizard := isEmptyLocal(existing) &&
		((setupCfg != nil && len(setupCfg.Questions) > 0) || len(conflicts) > 0)

	// Load journal once for the menu session.
	statePath := filepath.Join(baseDir, ".devbox", "deploy", "state.yml")
	state, err := journal.Load(statePath)
	if err != nil {
		state = nil
	}
	var pending *journal.PendingApply
	if state != nil {
		pending = state.Pending
	}

	// Build the deployable-services topology used by both the deploy-info banner
	// and the service submenu picker.
	items, topoErr := buildDeployItemsFn(baseDir, cfg, state)
	if topoErr != nil {
		// Non-fatal: render the menu without an ordered service list. The
		// underlying error will resurface in the proper diagnostic flow if/when
		// the user dispatches a deploy.
		items = nil
	}

	// Main loop: submenu cancels return here; top-level cancel/Exit ends.
	firstIteration := true
	for {
		// On every iteration except the first, scrub the previous frame so
		// the new banner + menu replace the old submenu rather than piling up.
		if !firstIteration {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), "\x1b[2J\x1b[H")
		}
		firstIteration = false

		// Print last-deploy info + pending banner above the menu form. The
		// banner is re-rendered each loop iteration so it reappears after a
		// submenu cancel-and-back.
		if banner := ui.RenderDeployInfo(state, time.Now(), deployInfoRowsFrom(items)); banner != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), banner)
		}
		if pending != nil {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderPendingBanner(pending))
		}

		choice, err := selectMenuItemFn(ctx, cmd, pending, showWizard)
		if err != nil {
			if errors.Is(err, setup.ErrWizardCanceled) || errors.Is(err, ui.ErrCancelled) {
				return nil
			}
			return err
		}

		switch choice {
		case menuRun:
			opts := deployRunOpts{ServiceName: "", NonInteractive: false, SkipPreflight: false}
			return runDeployRunFn(ctx, cmd, flags, opts)

		case menuRunService:
			gated := applyMandatoryGate(items)
			if len(gated) == 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.StyleWarning("No services with a deploy.yml found."))
				continue
			}
			name, err := selectDeployServiceFn(ctx, cmd, "Select service to deploy", gated, true)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					continue // back to main menu
				}
				return err
			}
			opts := deployRunOpts{ServiceName: name, NonInteractive: false, SkipPreflight: false}
			return runDeployRunFn(ctx, cmd, flags, opts)

		case menuPlan:
			opts := deployPlanOpts{ServiceName: "", Format: "table"}
			return runDeployPlanFn(ctx, cmd, flags, opts)

		case menuPlanService:
			if len(items) == 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.StyleWarning("No services with a deploy.yml found."))
				continue
			}
			name, err := selectDeployServiceFn(ctx, cmd, "Select service to plan", items, false)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					continue
				}
				return err
			}
			opts := deployPlanOpts{ServiceName: name, Format: "table"}
			return runDeployPlanFn(ctx, cmd, flags, opts)

		case menuWizard:
			// Surface non-port env problems (docker daemon, docker compose,
			// git/shell bin, project perms, checks.*, config.*) BEFORE the
			// user invests time in filling out the wizard. Port conflicts are
			// handled INSIDE the wizard, so we explicitly skip ports_free.
			if err := runPreWizardPreflightFn(ctx, cfg, baseDir, cmd.ErrOrStderr()); err != nil {
				return err
			}

			var questions []setup.Question
			if setupCfg != nil {
				questions = setupCfg.Questions
			}
			askQuestions, askPortOverrides, askServiceToggles := newHuhAskerFn(cmd.OutOrStdout())
			serviceToggles := buildWizardServiceToggles(cfg)

			// Wizard loop: after the wizard writes local.yml, re-load config
			// and re-probe ports. If the user picked a port that is also
			// occupied, re-ask for overrides on the new conflicts. Setup
			// questions and service toggles are asked exactly once on the
			// first iteration; subsequent iterations only re-ask ports.
			remainingConflicts := conflicts
			askedScopeOnce := false
			const maxIter = 5
			for iter := range maxIter {
				stepQuestions := questions
				stepToggles := serviceToggles
				if askedScopeOnce {
					stepQuestions = nil
					stepToggles = nil
				}
				deps := setup.WizardDeps{
					BaseDir:           baseDir,
					LocalPath:         localPath,
					Questions:         stepQuestions,
					PortConflicts:     remainingConflicts,
					ServiceToggles:    stepToggles,
					AskQuestions:      askQuestions,
					AskPortOverrides:  askPortOverrides,
					AskServiceToggles: askServiceToggles,
				}
				if err := runWizardFn(ctx, deps); err != nil {
					if errors.Is(err, setup.ErrWizardCanceled) {
						return nil
					}
					return fmt.Errorf("wizard failed: %w", err)
				}
				askedScopeOnce = true

				// Re-load config + re-probe ports against the freshly-written
				// local.yml. Any remaining conflicts mean the user picked a
				// new port that is also occupied; loop and re-ask.
				newCfg, err := config.LoadConfig(flags.ConfigPath)
				if err != nil {
					return fmt.Errorf("reload config after wizard: %w", err)
				}
				cfg = newCfg
				remainingConflicts, err = collectPortConflictsFn(ctx, cfg, baseDir)
				if err != nil {
					return fmt.Errorf("re-probing port conflicts: %w", err)
				}
				if len(remainingConflicts) == 0 {
					break
				}
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.StyleWarning(
					fmt.Sprintf("Port conflicts remain (%d); please pick different ports.", len(remainingConflicts)),
				))
				if iter == maxIter-1 {
					return fmt.Errorf("port conflicts still present after %d wizard iterations", maxIter)
				}
			}

			opts := deployRunOpts{ServiceName: "", NonInteractive: false, SkipPreflight: false}
			return runDeployRunFn(ctx, cmd, flags, opts)

		case menuExit:
			return nil

		default:
			return fmt.Errorf("unknown menu choice: %v", choice)
		}
	}
}

// isEmptyLocal returns true if the local config is missing or empty.
func isEmptyLocal(m map[string]any) bool {
	return len(m) == 0
}

// buildDeployServiceItems returns the deployable services (those with a
// deploy.yml) in deploy-order, decorated with type/mandatory/deploy-status.
// Services without deploy.yml are intentionally excluded — the run/plan
// per-service flow has nothing to do with them.
func buildDeployServiceItems(baseDir string, cfg *config.DevboxConfig, state *journal.ProjectState) ([]deployServiceItem, error) {
	if cfg == nil || len(cfg.Services) == 0 {
		return nil, nil
	}
	deploys, err := config.LoadServiceDeployConfigs(baseDir, cfg.Services)
	if err != nil {
		return nil, err
	}
	if len(deploys) == 0 {
		return nil, nil
	}
	genericDeploys := config.ServiceDeployConfigsToGeneric(deploys)
	order, err := deploy.TopoSortByAfter(genericDeploys, cfg.Services)
	if err != nil {
		// Fallback to alphabetical so the menu still works while the ordering
		// error surfaces through validate / preflight.
		order = make([]string, 0, len(deploys))
		for name := range deploys {
			order = append(order, name)
		}
		sort.Strings(order)
	}
	items := make([]deployServiceItem, 0, len(order))
	for _, name := range order {
		svc := cfg.Services[name]
		item := deployServiceItem{
			Name:      name,
			Type:      string(svc.Type),
			Icon:      svc.DisplayIcon(),
			Mandatory: svc.Required,
		}
		if state != nil {
			if s, ok := state.Services[name]; ok && s != nil {
				item.Deployed = s.Status == journal.StatusDeployed
				item.DeployedAt = s.DeployedAt
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// applyMandatoryGate locks optional items when at least one mandatory item is
// not yet deployed. Returns a fresh slice; the input is not mutated.
func applyMandatoryGate(in []deployServiceItem) []deployServiceItem {
	anyMandatoryNotDeployed := false
	for _, it := range in {
		if it.Mandatory && !it.Deployed {
			anyMandatoryNotDeployed = true
			break
		}
	}
	out := make([]deployServiceItem, len(in))
	copy(out, in)
	if !anyMandatoryNotDeployed {
		return out
	}
	for i := range out {
		if !out[i].Mandatory {
			out[i].Locked = true
			out[i].LockedHint = "deploy required services first"
		}
	}
	return out
}

// deployInfoRowsFrom converts deployServiceItem slice to UI rows.
func deployInfoRowsFrom(items []deployServiceItem) []ui.DeployInfoRow {
	if len(items) == 0 {
		return nil
	}
	rows := make([]ui.DeployInfoRow, 0, len(items))
	for _, it := range items {
		row := ui.DeployInfoRow{
			Name:        it.Name,
			Type:        it.Type,
			DeployedAt:  it.DeployedAt,
			NotDeployed: !it.Deployed,
		}
		if it.Deployed {
			row.Status = journal.StatusDeployed
		}
		rows = append(rows, row)
	}
	return rows
}

// selectMenuItemInteractive prompts the user to select a top-level menu item.
// Per-option descriptions are concatenated into the option label (so the help
// line stays available for navigation hints). Esc and Ctrl-C both abort with
// ui.ErrCancelled.
func selectMenuItemInteractive(_ context.Context, cmd *cobra.Command, _ *journal.PendingApply, showWizard bool) (menuChoice, error) {
	type itemDef struct {
		key         menuChoice
		label       string
		description string
	}

	var defs []itemDef
	// Wizard goes first when shown — on a fresh project it's almost always the
	// next step (answer setup questions / fix port conflicts before deploying).
	if showWizard {
		defs = append(defs, itemDef{menuWizard, "Wizard", "answer setup questions / pick port overrides, then deploy"})
	}
	defs = append(defs,
		itemDef{menuRun, "Run (all)", "deploy every enabled service in dependency order"},
		itemDef{menuRunService, "Run service…", "deploy one service with a deploy.yml"},
		itemDef{menuPlan, "Plan (all)", "preview the deploy plan without running anything"},
		itemDef{menuPlanService, "Plan service…", "preview the deploy plan for one service"},
		itemDef{menuExit, "Exit", "leave the deploy menu"},
	)

	// Right-pad the action label so descriptions line up across options.
	labelWidth := 0
	for _, d := range defs {
		if w := runeLen(d.label); w > labelWidth {
			labelWidth = w
		}
	}

	options := make([]huh.Option[string], 0, len(defs))
	for _, d := range defs {
		label := padRight(d.label, labelWidth)
		// Action label in accent, description muted (FG-only so huh's bold
		// wrapper on the selected row still wins).
		key := ui.StyleServiceOptionName("app", label) + "  " + ui.StyleOptionMuted(d.description)
		options = append(options, huh.NewOption(key, string(d.key)))
	}

	var choice string
	if len(options) > 0 {
		choice = options[0].Value
	}

	field := huh.NewSelect[string]().
		Title("Deploy").
		Options(options...).
		Value(&choice).
		Height(max(len(options)+5, 12))

	form := huh.NewForm(huh.NewGroup(field)).
		WithTheme(ui.Theme()).
		WithKeyMap(deployMenuKeyMap("exit")).
		WithShowHelp(true).
		WithOutput(cmd.OutOrStdout())

	if err := ui.RunWithPromptHooks(func() error { return form.Run() }); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return menuExit, nil
		}
		return "", fmt.Errorf("menu selection: %w", err)
	}

	return menuChoice(choice), nil
}

// deployMenuKeyMap builds the keymap used by both the top-level menu and the
// service picker. It customizes huh's built-in help so the rendered hint line
// includes ESC.
//
// huh hides the form-level Quit binding from field help, and for a single-
// group form it auto-disables Select.Prev/Next via WithPosition. So we hijack
// the Filter binding's display slot: filter functionality is not useful in a
// fixed N-item menu, and Filter stays enabled when not filtering — making it
// the one always-visible slot we can repurpose for the ESC hint. The Quit
// handler runs first at the form level, so binding ESC to Filter never
// triggers filter-mode entry; only Filter's help label is consumed.
//
// quitHelp is the verb shown after "esc" in the help line ("exit" / "back").
func deployMenuKeyMap(quitHelp string) *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", quitHelp))
	km.Select.Filter = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", quitHelp))
	// Make submit help read "enter select" instead of "enter submit" — purely
	// cosmetic.
	km.Select.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select"))
	return km
}

// selectDeployServiceInteractive prompts the user to pick a service from a
// pre-sorted, pre-decorated list. When applyGate is true, locked items are
// shown but selection is rejected via the form's Validate hook. Esc returns
// ui.ErrCancelled so the caller can navigate back to the parent menu.
func selectDeployServiceInteractive(_ context.Context, cmd *cobra.Command, title string, items []deployServiceItem, applyGate bool) (string, error) {
	if len(items) == 0 {
		return "", errors.New("no services to choose from")
	}

	// Pre-compute column widths so all rows align (status · type · name · meta).
	nameW := 0
	for _, it := range items {
		if w := runeLen(it.Name); w > nameW {
			nameW = w
		}
	}

	options := make([]huh.Option[string], 0, len(items))
	for _, it := range items {
		options = append(options, huh.NewOption(formatDeployServiceLabel(it, nameW), it.Name))
	}

	// First selectable (non-locked) choice as default, falling back to the
	// first item so the form opens on a sensible row.
	choice := items[0].Name
	for _, it := range items {
		if !it.Locked {
			choice = it.Name
			break
		}
	}

	field := huh.NewSelect[string]().
		Title(title).
		Options(options...).
		Value(&choice).
		Height(max(len(options)+5, 12))

	if applyGate {
		locked := make(map[string]string, len(items))
		for _, it := range items {
			if it.Locked {
				locked[it.Name] = it.LockedHint
			}
		}
		field = field.Validate(func(v string) error {
			if hint, ok := locked[v]; ok {
				return fmt.Errorf("locked: %s", hint)
			}
			return nil
		})
	}

	form := huh.NewForm(huh.NewGroup(field)).
		WithTheme(ui.Theme()).
		WithKeyMap(deployMenuKeyMap("back")).
		WithShowHelp(true).
		WithOutput(cmd.OutOrStdout())

	if err := ui.RunWithPromptHooks(func() error { return form.Run() }); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", ui.ErrCancelled
		}
		return "", fmt.Errorf("service selection: %w", err)
	}

	return choice, nil
}

// formatDeployServiceLabel renders one row in the service picker, mirroring
// the look of `devbox services`: service name colored by its type, type badge
// in matching color, secondary metadata in muted color. A leading status icon
// (✓ for deployed, · for not) gives at-a-glance deploy state without
// overriding the per-type palette.
func formatDeployServiceLabel(it deployServiceItem, nameWidth int) string {
	typeText := it.Type
	if typeText == "" {
		typeText = "-"
	}

	statusIcon := ui.StyleOptionMuted("·")
	if it.Deployed {
		statusIcon = ui.StyleOptionSuccess("✓")
	}

	name := padRight(it.Name, nameWidth)
	coloredName := ui.StyleServiceOptionName(it.Type, name)
	typeBadge := ui.StyleServiceOptionType(it.Type, "["+typeText+"]")

	meta := formatServiceMeta(it)
	if meta != "" {
		meta = "  " + ui.StyleServiceOptionContainer(meta)
	}

	return statusIcon + " " + ui.IconPrefix(it.Icon) + coloredName + " " + typeBadge + meta
}

// formatServiceMeta builds the secondary text shown after the type badge:
// mandatory/optional + deploy state + optional lock hint.
func formatServiceMeta(it deployServiceItem) string {
	var parts []string
	if it.Mandatory {
		parts = append(parts, "mandatory")
	} else {
		parts = append(parts, "optional")
	}
	if it.Deployed {
		when := "deployed"
		if !it.DeployedAt.IsZero() {
			when = "deployed " + relativeTimeForMenu(it.DeployedAt)
		}
		parts = append(parts, when)
	} else {
		parts = append(parts, "not deployed")
	}
	if it.Locked && it.LockedHint != "" {
		parts = append(parts, it.LockedHint)
	}
	return strings.Join(parts, " · ")
}

func runeLen(s string) int { return len([]rune(s)) }

func padRight(s string, w int) string {
	if n := runeLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// relativeTimeForMenu is a thin wrapper so menu code uses one timebase.
func relativeTimeForMenu(t time.Time) string {
	return ui.FormatRelativeTime(t, time.Now())
}

// buildWizardServiceToggles converts the project's manageable services into
// the typed list the wizard's multi-select consumes. Mirrors the filter used
// by `devbox services` (mandatory infra is hidden — it's always-on and not
// meaningful as a toggle row).
func buildWizardServiceToggles(cfg *config.DevboxConfig) []setup.ServiceToggle {
	if cfg == nil {
		return nil
	}
	rows := services.BuildRows(cfg)
	out := make([]setup.ServiceToggle, 0, len(rows))
	for _, r := range rows {
		out = append(out, setup.ServiceToggle{
			Name:      r.Name,
			Type:      r.Type,
			Icon:      r.Icon,
			Container: r.Container,
			Mandatory: r.Mandatory,
			Enabled:   r.Enabled,
		})
	}
	return out
}

// runPreWizardPreflight mirrors preflight.Run but explicitly skips the
// env.ports_free validator — port conflicts are the wizard's job to fix.
// Everything else (env.*, config.validate, checks.* for the deploy stage) is
// run identically to the post-wizard preflight, so any non-port issue is
// surfaced BEFORE the user invests time in filling out wizard questions.
//
// If any blocking diagnostic is produced, prints the diagnostic table to
// errOut and returns a deployValidationError so fang's err handler suppresses
// the double-print.
func runPreWizardPreflight(ctx context.Context, cfg *config.DevboxConfig, baseDir string, errOut io.Writer) error {
	cmdRegistry, _ := usercommands.LoadRegistryFromConfigPath(filepath.Join(baseDir, "devbox.yml"))
	validateCfg, warnings, loadErr := config.LoadValidateConfig(config.ValidateConfigPath(baseDir))

	vctx := validate.Context{
		Ctx:                 ctx,
		ProjectRoot:         baseDir,
		Cfg:                 cfg,
		CommandRegistry:     cmdRegistry,
		ValidateCfg:         validateCfg,
		ValidateCfgWarnings: warnings,
		ValidateCfgLoadErr:  loadErr,
		Stage:               "deploy",
	}

	reg := validate.NewRegistry()
	for _, v := range valenv.All(cfg) {
		if v.Domain() == "env" && v.ID() == "ports_free" {
			continue
		}
		reg.Register(v)
	}
	for _, v := range valconfig.All() {
		if v.Domain() == "config" && v.ID() == "validate" {
			reg.Register(v)
			break
		}
	}
	for _, v := range valchecks.AllForStage(validateCfg, nil, baseDir, cmdRegistry, "deploy") {
		reg.Register(v)
	}

	diags := reg.Run(vctx)
	rows := ui.FormatDiagnostics(diags, false)
	var filtered []ui.DiagnosticRow
	for _, r := range rows {
		if r.Severity != validate.SeverityOK {
			filtered = append(filtered, r)
		}
	}

	summary := validate.Aggregate(diags)
	blocking := validate.ExitCode(summary, false) != 0

	if len(filtered) > 0 {
		header := "Preflight (wizard prerequisites)"
		if blocking {
			header = ui.StyleFailed(header + " — blocking")
		} else {
			header = ui.StyleWarning(header)
		}
		_, _ = fmt.Fprintln(errOut, header)
		_, _ = fmt.Fprintln(errOut, ui.RenderDiagnosticsTable(filtered))
	}
	if blocking {
		return &deployValidationError{"preflight failed; fix the issues above before running the wizard"}
	}
	return nil
}

// usageError is a sentinel error type for usage/help errors (exit code 2).
type usageError string

func (u usageError) Error() string { return string(u) }
func (u usageError) ExitCode() int { return 2 }

// deployValidationError is returned when setup.yml validation blocks the deploy
// menu. It carries ExitCode() so fang's errHandler suppresses the double-print
// (diagnostics table is already written to stderr by the caller).
type deployValidationError struct{ msg string }

func (e *deployValidationError) Error() string { return e.msg }
func (e *deployValidationError) ExitCode() int { return 1 }
