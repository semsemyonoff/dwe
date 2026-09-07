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

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/project/services"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/secretsprompt"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	valchecks "github.com/semsemyonoff/dwe/internal/core/validate/checks"
	valconfig "github.com/semsemyonoff/dwe/internal/core/validate/config"
	valenv "github.com/semsemyonoff/dwe/internal/core/validate/env"
	valsecrets "github.com/semsemyonoff/dwe/internal/core/validate/secrets"
	valsetup "github.com/semsemyonoff/dwe/internal/core/validate/setup"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	"github.com/semsemyonoff/dwe/internal/core/workflow/setup"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

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
	keygateEnsureFn         = keygate.Ensure
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

// runDeployMenu is the entry point for `dwe deploy` without subcommands.
// It opens an interactive TUI menu if stdin/stdout are TTY, otherwise prints help.
func runDeployMenu(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	if !widgets.IsInteractiveFn(os.Stdin) {
		_ = cmd.Help()
		return usageError("dwe deploy: requires a subcommand or interactive TTY")
	}

	ctx := cmd.Context()
	baseDir := flags.ProjectRoot()
	localPath := filepath.Join(baseDir, "workspace", "local.yml")
	setupPath := filepath.Join(baseDir, "workspace", "setup.yml")

	// Offer the missing age identity BEFORE the menu's single config load, so
	// every action below — including `plan`, which would otherwise render
	// commands built from `<encrypted>` markers — works on decrypted values
	// without a reload.
	if err := ensureIdentity(cmd, flags, baseDir); err != nil {
		return err
	}

	cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
	if err != nil {
		return err
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
		rows := render.FormatDiagnostics(diags, false)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), render.DiagnosticsTable(rows))

		for _, diag := range diags {
			if diag.Severity >= validate.SeverityError {
				return &deployValidationError{"setup.yml has errors; fix them before deploying"}
			}
		}
	}

	showWizard := isEmptyLocal(existing) &&
		((setupCfg != nil && len(setupCfg.Questions) > 0) || len(conflicts) > 0)

	// Load journal once for the menu session.
	statePath := filepath.Join(baseDir, journal.DefaultRelPath)
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
		// submenu cancel-and-back. The subheader matches the shape used by the
		// services and docs selectors so users always know which dwe project
		// owns the prompt.
		render.PrintSelectorHeader(cmd.OutOrStdout(), cfg.Project.Name, "Deploy")
		if banner := render.DeployInfo(state, time.Now(), deployInfoRowsFrom(items)); banner != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), banner)
		}
		if pending != nil {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), render.PendingBanner(pending))
		}

		choice, err := selectMenuItemFn(ctx, cmd, pending, showWizard)
		if err != nil {
			if errors.Is(err, setup.ErrWizardCanceled) || errors.Is(err, widgets.ErrCancelled) {
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
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), styles.StyleWarning("No services with a deploy.yml found."))
				continue
			}
			name, err := selectDeployServiceFn(ctx, cmd, "Select service to deploy", gated, true)
			if err != nil {
				if errors.Is(err, widgets.ErrCancelled) {
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
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), styles.StyleWarning("No services with a deploy.yml found."))
				continue
			}
			name, err := selectDeployServiceFn(ctx, cmd, "Select service to plan", items, false)
			if err != nil {
				if errors.Is(err, widgets.ErrCancelled) {
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
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), styles.StyleWarning(
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

// ensureIdentity runs the shared age-identity gate for the whole menu session.
//
// The raw-layer error is deliberately dropped: nil layers make the gate skip
// itself, and the LoadConfigOrWrap right below reports the very same file with
// today's `loading config: …` wording. The gate must never be the surface that
// speaks about a broken config.
func ensureIdentity(cmd *cobra.Command, flags *cmdctx.RootFlags, baseDir string) error {
	layers, _ := config.LoadRawLayers(flags.ConfigPath)
	in, out := cmd.InOrStdin(), cmd.OutOrStdout()

	_, err := keygateEnsureFn(cmd.Context(), keygate.Options{
		BaseDir:        baseDir,
		Layers:         layers,
		Interactive:    widgets.IsInteractiveFn(in),
		OutputJSON:     flags.Output == "json",
		NonInteractive: cmdctx.NonInteractiveEnv(),
		Prompt: func(ctx context.Context, recipient string) (secrets.Identity, error) {
			return secretsprompt.PromptIdentity(ctx, recipient, in, out)
		},
		Confirm: func(ctx context.Context, explanation string) (bool, error) {
			return secretsprompt.ConfirmImport(ctx, explanation, in, out)
		},
		Out: out,
	})
	if err == nil {
		return nil
	}
	// The three refusals are the same user-facing situation `dwe secrets`
	// reports as secrets_no_identity: this project needs a key this machine
	// does not usably have.
	if errors.Is(err, keygate.ErrAborted) ||
		errors.Is(err, keygate.ErrEnvSourceUnusable) ||
		errors.Is(err, keygate.ErrKeyfileUnusable) {
		coded := cmdctx.ErrWrap("secrets_no_identity", err)
		if recipient := config.RecipientFromLayers(layers); recipient != "" {
			coded = coded.WithDetail("recipient", recipient).WithHint(secrets.IdentityHint(recipient))
		}
		return coded
	}
	return err
}

// isEmptyLocal returns true if the local config is missing or empty.
func isEmptyLocal(m map[string]any) bool {
	return len(m) == 0
}

// buildDeployServiceItems returns the deployable services (those with a
// deploy.yml) in deploy-order, decorated with type/mandatory/deploy-status.
// Services without deploy.yml are intentionally excluded — the run/plan
// per-service flow has nothing to do with them.
func buildDeployServiceItems(baseDir string, cfg *config.DweConfig, state *journal.ProjectState) ([]deployServiceItem, error) {
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
func deployInfoRowsFrom(items []deployServiceItem) []render.DeployInfoRow {
	if len(items) == 0 {
		return nil
	}
	rows := make([]render.DeployInfoRow, 0, len(items))
	for _, it := range items {
		row := render.DeployInfoRow{
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
// widgets.ErrCancelled.
func selectMenuItemInteractive(ctx context.Context, cmd *cobra.Command, _ *journal.PendingApply, showWizard bool) (menuChoice, error) {
	field := buildMenuField(showWizard)

	result, err := ask.Run(ctx, "Deploy", []ask.Field{field}, ask.RunOptions{
		Output:     cmd.OutOrStdout(),
		Quit:       &ask.QuitSpec{Keys: []string{"q", "esc", "ctrl+c"}, Help: "exit"},
		SubmitHelp: "select",
	})
	if err != nil {
		return mapMenuSelectionErr(err)
	}

	return menuChoice(result.String("choice")), nil
}

// mapMenuSelectionErr translates a menu-selection error: widgets.ErrCancelled
// maps to (menuExit, nil), preserving today's "esc/q exits the top menu
// cleanly" semantics; any other error is wrapped for the caller.
func mapMenuSelectionErr(err error) (menuChoice, error) {
	if errors.Is(err, widgets.ErrCancelled) {
		return menuExit, nil
	}
	return "", fmt.Errorf("menu selection: %w", err)
}

// menuItemDef describes one top-level menu option before it is turned into
// an ask.Option label.
type menuItemDef struct {
	key         menuChoice
	label       string
	description string
}

// menuItemDefs returns the top-level menu items in display order. Wizard
// goes first when shown — on a fresh project it's almost always the next
// step (answer setup questions / fix port conflicts before deploying).
func menuItemDefs(showWizard bool) []menuItemDef {
	var defs []menuItemDef
	if showWizard {
		defs = append(defs, menuItemDef{menuWizard, "Wizard", "answer setup questions / pick port overrides, then deploy"})
	}
	defs = append(defs,
		menuItemDef{menuRun, "Run (all)", "deploy every enabled service in dependency order"},
		menuItemDef{menuRunService, "Run service…", "deploy one service with a deploy.yml"},
		menuItemDef{menuPlan, "Plan (all)", "preview the deploy plan without running anything"},
		menuItemDef{menuPlanService, "Plan service…", "preview the deploy plan for one service"},
		menuItemDef{menuExit, "Exit", "leave the deploy menu"},
	)
	return defs
}

// buildMenuField assembles the ask.Field for the top-level menu select:
// per-option descriptions are concatenated into the option label (so the
// help line stays available for navigation hints), and the first item is
// preselected.
func buildMenuField(showWizard bool) ask.Field {
	defs := menuItemDefs(showWizard)

	// Right-pad the action label so descriptions line up across options.
	labelWidth := 0
	for _, d := range defs {
		if w := render.RuneLen(d.label); w > labelWidth {
			labelWidth = w
		}
	}

	options := make([]ask.Option, 0, len(defs))
	for _, d := range defs {
		label := render.PadRight(d.label, labelWidth)
		// Action label in accent, description muted (FG-only so huh's bold
		// wrapper on the selected row still wins).
		optLabel := styles.StyleServiceOptionName("app", label) + "  " + styles.StyleOptionMuted(d.description)
		options = append(options, ask.Option{Value: string(d.key), Label: optLabel})
	}

	var def string
	if len(options) > 0 {
		def = options[0].Value
	}

	return ask.Field{
		Key:     "choice",
		Kind:    ask.FieldSelect,
		Options: options,
		Default: def,
		Height:  max(len(options)+5, 12),
	}
}

// selectDeployServiceInteractive prompts the user to pick a service from a
// pre-sorted, pre-decorated list. When applyGate is true, locked items are
// shown but selection is rejected via the form's Validate hook. Esc returns
// widgets.ErrCancelled so the caller can navigate back to the parent menu.
func selectDeployServiceInteractive(ctx context.Context, cmd *cobra.Command, title string, items []deployServiceItem, applyGate bool) (string, error) {
	if len(items) == 0 {
		return "", errors.New("no services to choose from")
	}

	field := buildServiceField(items, applyGate)

	result, err := ask.Run(ctx, title, []ask.Field{field}, ask.RunOptions{
		Output:     cmd.OutOrStdout(),
		Quit:       &ask.QuitSpec{Keys: []string{"q", "esc", "ctrl+c"}, Help: "back"},
		SubmitHelp: "select",
	})
	if err != nil {
		return mapServiceSelectionErr(err)
	}

	return result.String("service"), nil
}

// mapServiceSelectionErr translates a service-selection error:
// widgets.ErrCancelled passes through unchanged so the caller can navigate
// back to the parent menu; any other error is wrapped.
func mapServiceSelectionErr(err error) (string, error) {
	if errors.Is(err, widgets.ErrCancelled) {
		return "", widgets.ErrCancelled
	}
	return "", fmt.Errorf("service selection: %w", err)
}

// buildServiceField assembles the ask.Field for the service picker from a
// pre-sorted, pre-decorated item list. When applyGate is true, locked items
// are shown but selection is rejected via Validate; the first non-locked
// item is preselected so an initial Enter never hits a locked row.
func buildServiceField(items []deployServiceItem, applyGate bool) ask.Field {
	// Pre-compute column widths so all rows align (status · type · name · meta).
	nameW := 0
	for _, it := range items {
		if w := render.RuneLen(it.Name); w > nameW {
			nameW = w
		}
	}

	options := make([]ask.Option, 0, len(items))
	for _, it := range items {
		options = append(options, ask.Option{Value: it.Name, Label: formatDeployServiceLabel(it, nameW)})
	}

	// First selectable (non-locked) choice as default, falling back to the
	// first item so the form opens on a sensible row.
	def := items[0].Name
	for _, it := range items {
		if !it.Locked {
			def = it.Name
			break
		}
	}

	field := ask.Field{
		Key:     "service",
		Kind:    ask.FieldSelect,
		Options: options,
		Default: def,
		Height:  max(len(options)+5, 12),
	}

	if applyGate {
		locked := make(map[string]string, len(items))
		for _, it := range items {
			if it.Locked {
				locked[it.Name] = it.LockedHint
			}
		}
		field.Validate = func(v string) error {
			if hint, ok := locked[v]; ok {
				return fmt.Errorf("locked: %s", hint)
			}
			return nil
		}
	}

	return field
}

// formatDeployServiceLabel renders one row in the service picker, mirroring
// the look of `dwe services`: service name colored by its type, type badge
// in matching color, secondary metadata in muted color. A leading status icon
// (✓ for deployed, · for not) gives at-a-glance deploy state without
// overriding the per-type palette.
func formatDeployServiceLabel(it deployServiceItem, nameWidth int) string {
	typeText := it.Type
	if typeText == "" {
		typeText = "-"
	}

	statusIcon := styles.StyleOptionMuted("·")
	if it.Deployed {
		statusIcon = styles.StyleOptionSuccess("✓")
	}

	name := render.PadRight(it.Name, nameWidth)
	coloredName := styles.StyleServiceOptionName(it.Type, name)
	typeBadge := styles.StyleServiceOptionType(it.Type, "["+typeText+"]")

	meta := formatServiceMeta(it)
	if meta != "" {
		meta = "  " + styles.StyleServiceOptionContainer(meta)
	}

	return statusIcon + " " + styles.IconPrefix(it.Icon) + coloredName + " " + typeBadge + meta
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

// relativeTimeForMenu is a thin wrapper so menu code uses one timebase.
func relativeTimeForMenu(t time.Time) string {
	return render.FormatRelativeTime(t, time.Now())
}

// buildWizardServiceToggles converts the project's manageable services into
// the typed list the wizard's multi-select consumes. Mirrors the filter used
// by `dwe services` (mandatory infra is hidden — it's always-on and not
// meaningful as a toggle row).
func buildWizardServiceToggles(cfg *config.DweConfig) []setup.ServiceToggle {
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
func runPreWizardPreflight(ctx context.Context, cfg *config.DweConfig, baseDir string, errOut io.Writer) error {
	cmdRegistry, _ := usercommands.LoadRegistryFromConfigPath(filepath.Join(baseDir, "workspace.yml"))
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
	// Mirrors preflight.Run's second cherry-pick: readiness, not content. An
	// undecryptable secret must stop the user before the wizard, not after the
	// questions have been answered.
	reg.Register(valsecrets.UnresolvedValidator())
	for _, v := range valchecks.AllForStage(validateCfg, nil, baseDir, cmdRegistry, "deploy", cfg.Services, false) {
		reg.Register(v)
	}

	diags := reg.Run(vctx)
	rows := render.FormatDiagnostics(diags, false)
	var filtered []render.DiagnosticRow
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
			header = styles.StyleFailed(header + " — blocking")
		} else {
			header = styles.StyleWarning(header)
		}
		_, _ = fmt.Fprintln(errOut, header)
		_, _ = fmt.Fprintln(errOut, render.DiagnosticsTable(filtered))
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
