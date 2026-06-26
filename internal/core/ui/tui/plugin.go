package tui

import tea "charm.land/bubbletea/v2"

// PanelID identifies one body panel within a plugin's layout. It is opaque to
// the framework: the plugin assigns IDs in [Plugin.Panels] and the framework
// echoes them back through [Plugin.ViewPanel] and the focus manager. IDs must be
// unique within a single plugin's panel set.
type PanelID string

// Panel declares one body panel: its identity, its border title, and its
// horizontal split weight. The framework lays panels out left→right by weight
// (see [layoutPanels]) and computes each inner region.
//
// Weight must be a positive integer; [newFrame] (Task 7) validates the panel
// set before launch (non-empty, all weights positive), so [layoutPanels] and
// [Plugin.ViewPanel] never see a degenerate layout.
//
// ID and Weight are load-bearing in Stage 0. Title is a DOCUMENTED PLACEHOLDER
// (like the registry's Aliases/Rebindable/Mouse): it exists now so callers can
// be written against the final shape, but Stage 0 renders plain borders and
// never draws it — lipgloss v2 has no border-title primitive, so the title
// renderer lands with the real frame stage.
type Panel struct {
	// ID uniquely identifies this panel within the plugin.
	ID PanelID
	// Title is the placeholder for the panel's top-border label. English here;
	// the migration stages route it through i18n. Reserved for a later stage:
	// Stage 0 does not render it (see the type doc).
	Title string
	// Weight is the relative horizontal share of the body width. A two-panel
	// plugin with weights {1, 2} gives the second panel twice the first's width
	// (remainder lands on the last panel — see [layoutPanels]).
	Weight int
}

// Overlay is one modal layer (help, future inspect/filter) composited centred
// over the body region. It carries its pre-rendered content plus the cell
// dimensions that content occupies, so the overlay manager (Task 5) can centre
// it without re-measuring ANSI width.
//
// This is the canonical definition. Task 5's overlayStack and Composite operate
// over THIS type; they do not redefine it.
type Overlay struct {
	// Content is the pre-rendered, possibly multi-line, possibly ANSI-styled
	// overlay body.
	Content string
	// Width / Height are the cell dimensions Content occupies. The overlay
	// manager uses them to centre the overlay over the body region.
	Width, Height int
}

// Plugin is the contract every full-screen surface implements to run inside the
// [Frame]. The framework owns chrome (borders, status line, overlays, the
// terminal envelope) and geometry; the plugin owns body content and behaviour.
//
// Contract status: PINNED, not frozen. The method set is stable enough for the
// Stage 3 pilot to build against, but the Stage 3 migration may feed one
// revision back before the contract is frozen for Stages 4–5b (spec § 7).
//
// View contract split: a plugin renders each panel's body as a STRING via
// [Plugin.ViewPanel]; only [Frame] (the tea.Model) returns a tea.View and owns
// its envelope fields (AltScreen, MouseMode, Cursor). Plugins never construct a
// tea.View. (In bubbletea/v2 v2.0.7, Model.View returns tea.View, not a string;
// see cmdbrowser/model.go and statustui/tui.go.)
type Plugin interface {
	// Init returns the plugin's startup command, run once when the program
	// starts. The framework batches it with its own startup work.
	Init() tea.Cmd

	// Close releases plugin resources. The launch helper (Task 8) defers it so it
	// runs on normal quit AND error/interrupt paths.
	Close() error

	// Resize is called on every terminal resize with the overall INNER body
	// region (the content area inside the frame chrome). Plugins that cache
	// layout off the body size use it; per-panel inner regions arrive separately
	// through [Plugin.ViewPanel].
	Resize(body Region)

	// Update routes a message to the plugin and returns any follow-up command.
	// The framework forwards every non-key message here (async preservation) and
	// forwards key messages it did not handle itself.
	Update(msg tea.Msg) tea.Cmd

	// ViewPanel renders the body content of one panel into the inner region the
	// framework computed for it. The framework draws the border/focus around the
	// returned string; the plugin must not draw the frame border. inner gives the
	// exact cell box the content should fill.
	ViewPanel(id PanelID, inner Region) string

	// Panels declares the plugin's body panels left→right. A single-region plugin
	// (e.g. status) returns exactly one panel. Must be non-empty with all weights
	// positive; [newFrame] validates this before launch.
	Panels() []Panel

	// StatusContext returns the plugin's segment of the bottom status line (the
	// middle zone; the frame supplies brand/project on the left and the help hint
	// on the right).
	StatusContext() string

	// Actions registers the plugin's actions into the shared registry. It returns
	// the registry's duplicate-action/duplicate-key error so [newFrame]/[Run] can
	// fail BEFORE launch rather than at render time.
	Actions(reg *Registry) error

	// HandleAction dispatches an action the registry matched to a physical key.
	// The bool reports whether the plugin handled it. Built-in actions
	// (help/focus/quit) are framework-handled and never reach the plugin.
	HandleAction(a Action) (tea.Cmd, bool)

	// PendingOverlay reports an overlay the plugin wants shown. The framework
	// drains it after a HandleAction call and after an Update forward, so an
	// action-triggered overlay appears immediately. The bool reports presence.
	PendingOverlay() (Overlay, bool)

	// Result returns the plugin's typed outcome, returned UNCHANGED by [Run]
	// (no wrapper type). Callers type-assert it to the concrete surface's result.
	Result() any
}
