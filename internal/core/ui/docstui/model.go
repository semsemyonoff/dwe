package docstui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/mermaid"
	"github.com/semsemyonoff/dwe/internal/core/docs/render"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Model holds the docs browser's internal state. It is embedded in browser to
// share Tree/Viewport/Filter/DiagramState/heading-index/loaded-topic fields
// between the legacy standalone path and the plugin path.
type Model struct {
	// TUI state
	Tree       *TreeWidget
	Viewport   *ViewportWidget
	StatusBar  *StatusBarWidget
	Filter     *TreeFilter
	Translator i18n.Translator
	Locale     string
	Theme      string
	Title      string // Brand-bar title; "DWE · <project> · Documentation".

	// Data
	Roots []docs.DocRoot

	// Rendering
	MermaidRenderer mermaid.Renderer

	// Current state
	CurrentTopic      *TreeNode
	FocusZone         FocusZone
	ContentWidth      int
	ContentHeight     int
	TermWidth         int
	TermHeight        int
	DiagramState      *DiagramState
	TmuxHintShown     bool
	AvailableLocales  []string // Available languages for the current file
	CurrentSourceLang string   // The actual source language (en if fallback, otherwise requested)
	Watcher           *Watcher // File change watcher (project docs only)
	ProjectRoot       string   // Path to the project root

	// MmdcMissingNotice is the install guidance surfaced in the diagram
	// render-error overlay (opened with `E`) when mmdc is missing on $PATH and
	// the mermaid renderer is not explicitly disabled. Empty means `E` has
	// nothing to show. Populated by runDocsTUI before any topic is loaded. Kept
	// off the rendered content deliberately — the old global banner nagged on
	// every topic; the on-demand overlay surfaces the same guidance only when a
	// user actually reaches for a disabled diagram.
	MmdcMissingNotice string

	// Background rendering
	Prefetch         *Prefetch
	PrefetchProgress ProgressMsg
	prefetchChan     chan ProgressMsg
	// progressReaderLive guards against spawning a second waitForProgress reader
	// on every diagram-topic load: the reader chain is self-sustaining (the
	// ProgressMsg handler always respawns one reader on the shared channel), so
	// only the first load starts it — later loads would otherwise leak one parked
	// goroutine per visit (reclaimed only at Close).
	progressReaderLive bool

	// Tracks the last topic loaded into the viewport so heading navigation
	// can skip a redundant re-render when the cursor jumps between headings
	// of the same file.
	currentlyLoadedPath   string
	currentlyLoadedLocale string

	// loadGen increments on every loadTopic call. Each background load
	// captures its generation; topicLoadedMsg handlers drop messages whose
	// generation doesn't match m.loadGen so a fast-scrolling user only sees
	// the result of their *latest* selection, not stale renders from prior
	// in-flight topic loads.
	loadGen int

	// pendingHeadingIdx carries a heading target (source index) across an
	// async topic load so the viewport can scroll to it once the rendered
	// content arrives. -1 means "no pending scroll". Cleared by the
	// topicLoadedMsg handler after applying.
	pendingHeadingIdx int

	// Cached glamour output + diagram metadata so prefetch progress events
	// can re-run diagram inlining without re-rendering glamour.
	lastRenderedOutput   string
	lastRenderedDiagrams []render.DiagramRef

	// currentHeadingLines maps each source H2/H3 heading index to the line
	// number in the rendered viewport where it appears. Built per-load by
	// stripHeadingMarkers (see heading_anchors.go) — substring matching on
	// heading text is fragile because body prose often repeats heading
	// words; this map is authoritative.
	currentHeadingLines []int

	// currentDiagramLines maps each diagram index to the rendered viewport
	// line where its placeholder sits (in document order, ascending). Built
	// per-load from the diagram markers in the rendered output and used by
	// syncActiveDiagram to make the diagram under the cursor the active one.
	currentDiagramLines []int

	// viewportCursor is the rendered-row index of the reading cursor in the
	// right viewport panel (revdiff-style line cursor). Because glamour has
	// already wrapped content into final rows, this index IS the visible row
	// directly — no logical→visual mapping is needed. It anchors the
	// diagram-under-cursor selection (syncActiveDiagram) and link activation,
	// and is reset/clamped on every topic load. The glyph is drawn by the
	// browser only while the viewport is focused.
	viewportCursor int

	// currentLinks holds the OSC-8 hyperlink regions of the displayed document
	// (post-inlineDiagrams), one entry per (rendered row × link). Built per-load
	// and used for click / Enter link activation; cleared on error/blank loads.
	currentLinks []linkRegion

	// Per-session temp dir for "open diagram" exports. One dir per Model so
	// concurrent `dwe docs` sessions don't race on the same temp filename.
	// Lazily created on first export; left on disk at exit (os cleans /tmp).
	diagramExportDir string

	ctx context.Context
}

// FocusZone identifies which panel of the standalone docs model has keyboard focus.
type FocusZone int

// Focus zone values: tree panel or viewport panel.
const (
	FocusTree FocusZone = iota
	FocusViewport
)

// NewModel constructs the standalone docs browser model used by the docstui.Run path.
func NewModel(ctx context.Context, roots []docs.DocRoot, locale string, translator i18n.Translator, renderer mermaid.Renderer, termWidth, termHeight int, projectRoot, title, mermaidTheme string) (*Model, error) {
	treeWidget, err := NewTreeWidget(roots, locale)
	if err != nil {
		return nil, err
	}

	theme := resolveMermaidTheme(mermaidTheme)

	contentW := viewportInnerWidth(termWidth)
	contentH := viewportInnerHeight(termHeight)

	m := &Model{
		Roots:             roots,
		Tree:              treeWidget,
		Locale:            locale,
		Translator:        translator,
		MermaidRenderer:   renderer,
		Filter:            NewTreeFilter(),
		Theme:             theme,
		Title:             title,
		FocusZone:         FocusTree,
		TermWidth:         termWidth,
		TermHeight:        termHeight,
		ContentWidth:      contentW,
		ContentHeight:     contentH,
		Viewport:          NewViewportWidget(contentW, contentH),
		StatusBar:         NewStatusBarWidget(),
		DiagramState:      NewDiagramState(nil),
		AvailableLocales:  []string{},
		CurrentSourceLang: "en",
		ProjectRoot:       projectRoot,
		PrefetchProgress:  ProgressMsg{},
		pendingHeadingIdx: -1,
		ctx:               ctx,
	}

	// Create watcher for project docs if project path is provided
	if projectRoot != "" {
		projectDocsPath := filepath.Join(projectRoot, "docs")
		_, err := os.Stat(projectDocsPath)
		if err == nil {
			// Project docs exist; create a watcher
			watcher, err := NewWatcher(ctx, projectDocsPath)
			if err == nil {
				m.Watcher = watcher
			}
		}
	}

	// Set the initial cursor so the browser knows which topic to load on the
	// first WindowSizeMsg. The load itself is deferred to browser.Update so
	// it uses the correct framework-supplied width (Decision #10).
	if m.Tree.Cursor() != nil {
		m.CurrentTopic = m.Tree.Cursor()
	}

	return m, nil
}

// topicLoadedMsg carries the result of a background topic resolve+render.
// Generation is matched against m.loadGen so the model only applies the
// latest user-driven load — stale messages from prior selections are
// dropped (the user already navigated elsewhere).
type topicLoadedMsg struct {
	Generation   int
	Path         string
	Locale       string
	SourceLang   string
	RootName     string // "dwe" or "project" — gates the localization chrome
	Stale        bool
	Output       string
	Diagrams     []render.DiagramRef
	HeadingLines []int // rendered-line index per source H2/H3 heading
	Err          error
}

// translationBanner returns the markdown blockquote to prepend to a topic when
// its translation is missing (fell back to English) or outdated, or "" when no
// banner applies. Banners are specific to dwe's own docs (rootName == "dwe"):
// only those ship the i18n tree and the content-hash staleness manifest, so a
// non-en locale falling back to English in a *project's* docs is expected and
// must not be flagged. locale is the requested locale, sourceLang the locale
// actually resolved, and stale the content-hash staleness verdict.
func translationBanner(rootName, locale, sourceLang string, stale bool) string {
	if rootName != "dwe" {
		return ""
	}
	switch {
	case sourceLang != locale:
		return "> **ℹ Translation not available for `" + locale + "`. Showing English version.**\n\n"
	case stale:
		return "> **⚠ This translation is outdated (last synced at previous version, current is newer). Press `e` to view the English version.**\n\n"
	}
	return ""
}

// loadTopic kicks off a background load for the given tree node. The fast
// prep (progress reset, available-locales scan, source-root lookup) runs
// synchronously; the slow ResolveContent + glamour Render runs in a
// goroutine and posts a topicLoadedMsg back. The viewport keeps its current
// content during the load so the user sees no flicker; the topicLoadedMsg
// handler swaps in the rendered output when it arrives.
func (m *Model) loadTopic(node *TreeNode) (tea.Cmd, error) {
	// Advance the prefetch generation on every topic transition — including
	// directories and no-diagram files — so any in-flight ProgressMsg from
	// the previously loaded topic is rejected by the Update handler.
	if m.Prefetch != nil {
		m.Prefetch.BeginTopic()
	}
	m.PrefetchProgress = ProgressMsg{}
	m.StatusBar.SetProgress(0)
	m.loadGen++

	// Resolve the markdown that backs this row: the file node itself, the
	// folded index.md for a directory that has one, or nothing for a plain
	// directory (blank viewport).
	cn := contentNodeFor(node)
	if cn == nil {
		m.Viewport.SetContent("")
		m.AvailableLocales = []string{}
		m.CurrentSourceLang = "en"
		m.lastRenderedOutput = ""
		m.lastRenderedDiagrams = nil
		m.currentLinks = nil
		m.currentlyLoadedPath = ""
		m.currentlyLoadedLocale = ""
		m.StatusBar.SetPath("")
		m.pendingHeadingIdx = -1
		// The viewport is now blank, so drop any diagram/heading state carried
		// from the previous topic — otherwise `y`/`[`/`]` would copy or cycle the
		// stale diagram of a file we are no longer showing.
		m.DiagramState = NewDiagramState(nil)
		m.currentDiagramLines = nil
		m.currentHeadingLines = nil
		return nil, nil
	}

	path := cn.Path
	m.AvailableLocales = docs.AvailableLocalesFor(m.Roots, path)

	var sourceRoot docs.DocRoot
	if node.RootName != "" {
		sourceRoot = docs.RootByName(m.Roots, node.RootName)
	}
	if sourceRoot.Name == "" {
		topicPath := strings.TrimSuffix(path, ".md")
		resolved, err := docs.Resolve(m.Roots, topicPath, m.Locale)
		if err != nil {
			m.Viewport.SetContent("Error: " + err.Error())
			return nil, err
		}
		sourceRoot = docs.RootByName(m.Roots, resolved.Source)
	}

	// Snapshot everything the goroutine needs so we never touch m from
	// outside the bubbletea event loop.
	gen := m.loadGen
	locale := m.Locale
	theme := m.Theme
	width := m.ContentWidth
	rootName := sourceRoot.Name

	return func() tea.Msg {
		content, sourceLang, stale, err := docs.ResolveContent(sourceRoot, path, locale)
		if err != nil {
			return topicLoadedMsg{Generation: gen, Path: path, Locale: locale, Err: err}
		}

		contentToRender := content
		if banner := translationBanner(rootName, locale, sourceLang, stale); banner != "" {
			contentToRender = append([]byte(banner), content...)
		}

		placeholderFunc := func(index int) render.MermaidPlaceholder {
			return render.MermaidPlaceholder{Text: diagramMarker(index)}
		}
		opts := render.Opts{Theme: theme, Width: width}

		// Inject heading anchors before glamour so the post-render pass
		// can build a reliable index→rendered-line map for section scrolls.
		// Substring matching on heading text is too fragile: body prose
		// routinely references the same words.
		marked := preprocessHeadings(contentToRender)
		result, rerr := render.Render(marked, opts, placeholderFunc)
		if rerr != nil {
			return topicLoadedMsg{Generation: gen, Path: path, Locale: locale, Err: rerr}
		}
		clean, headingLines := stripHeadingMarkers(string(result.Output))
		return topicLoadedMsg{
			Generation:   gen,
			Path:         path,
			Locale:       locale,
			SourceLang:   sourceLang,
			RootName:     rootName,
			Stale:        stale,
			Output:       clean,
			Diagrams:     result.Diagrams,
			HeadingLines: headingLines,
		}
	}, nil
}

// applyTopicLoaded applies the result of a background topic load. Stale
// generations are silently dropped — a faster selection has already started
// or finished. Successful loads update the viewport, status bar, diagram
// state, and queue diagrams for the prefetch worker pool. A pending heading
// scroll (captured at selectCursor time) fires here so heading navigation
// works whether or not the file was already loaded.
func (m *Model) applyTopicLoaded(msg topicLoadedMsg) tea.Cmd {
	if msg.Generation != m.loadGen {
		return nil
	}
	if msg.Err != nil {
		m.Viewport.SetContent("Error: " + msg.Err.Error())
		m.pendingHeadingIdx = -1
		m.currentLinks = nil
		return nil
	}

	m.CurrentSourceLang = msg.SourceLang
	m.lastRenderedOutput = msg.Output
	m.lastRenderedDiagrams = msg.Diagrams
	m.currentHeadingLines = msg.HeadingLines
	m.currentDiagramLines = diagramLineIndices(msg.Output, len(msg.Diagrams))
	displayed := m.inlineDiagrams(msg.Output, msg.Diagrams)
	m.Viewport.SetContent(displayed)
	// Parse the displayed string (post-inline) so link columns line up with what
	// the user clicks; the active/inactive diagram placeholders are equal-width so
	// a later syncActiveDiagram re-inline never shifts these regions.
	m.currentLinks = parseLinkRegions(displayed)
	m.StatusBar.SetPath(msg.Path)
	// The [lang] tag is dwe-docs chrome (they carry the i18n tree). Project docs
	// have no localization model, so drop it there rather than always tagging [en].
	if msg.RootName == "dwe" {
		m.StatusBar.SetLanguage(msg.SourceLang)
	} else {
		m.StatusBar.SetLanguage("")
	}
	m.currentlyLoadedPath = msg.Path
	m.currentlyLoadedLocale = msg.Locale
	m.DiagramState = NewDiagramState(msg.Diagrams)

	if m.pendingHeadingIdx >= 0 {
		m.scrollToHeading(m.pendingHeadingIdx)
		m.pendingHeadingIdx = -1
	} else {
		m.Viewport.ScrollToLine(0)
	}

	// Park the reading cursor at the top of the landed view (top of page, or the
	// heading we scrolled to) so it is on-screen and anchors diagram selection.
	m.viewportCursor = m.clampCursor(m.Viewport.YOffset())

	// Make the diagram under the cursor the active one for the position we
	// landed on.
	m.syncActiveDiagram()

	if len(msg.Diagrams) == 0 {
		return nil
	}
	// Skip prefetch when the renderer can't cache results — i.e. the
	// chain reduces to mermaid.Disabled (mode=off, or auto/mmdc with
	// mmdc absent on $PATH). Without this gate the worker pool would
	// fail every queued item in a rapid burst, and each ProgressMsg
	// rewrites the full viewport via inlineDiagrams + SetContent —
	// the compounded re-renders are what users perceive as a UI
	// "freeze on first open" of a diagram-heavy doc. Lookuper is the
	// cache-capable contract; the diagramPlaceholder fallback (no
	// Lookuper → "rendering disabled") already surfaces the right
	// hint, so a silent skip here is the correct user-facing behavior.
	if _, ok := m.MermaidRenderer.(mermaid.Lookuper); !ok {
		return nil
	}
	m.ensurePrefetch()
	items := make([]WorkItem, len(msg.Diagrams))
	theme := m.diagramTheme()
	for i, diag := range msg.Diagrams {
		items[i] = WorkItem{
			Source: diag.Source,
			Theme:  theme,
			Width:  diagramRenderWidth(),
			Index:  i,
		}
	}
	m.Prefetch.Queue(items)
	// The reader chain is respawned by the ProgressMsg handler, so start it at
	// most once — the already-live reader picks up this topic's messages on the
	// shared channel. Spawning again here would leak a parked goroutine per load.
	if m.progressReaderLive {
		return nil
	}
	m.progressReaderLive = true
	return waitForProgress(m.prefetchChan)
}

// applyLocaleChange rebuilds the tree with the new locale so per-file
// titles and heading sub-rows reflect the translated text, then re-loads
// the current topic. Rebuild preserves the cursor by (RootName, Path) plus
// the heading index when applicable, so the user stays on the same row.
// Heading scroll is deferred to the topicLoadedMsg handler via
// pendingHeading so it lands after the translated content arrives.
func (m *Model) applyLocaleChange() tea.Cmd {
	if m.Tree != nil {
		_ = m.Tree.Rebuild(m.Locale)
		m.CurrentTopic = m.Tree.Cursor()
	}
	if m.CurrentTopic == nil {
		return nil
	}
	if idx := headingIndex(m.CurrentTopic); idx >= 0 {
		m.pendingHeadingIdx = idx
	}
	cmd, _ := m.loadTopic(m.CurrentTopic)
	return cmd
}

// selectCursor handles tree cursor movement: loads the file backing the
// current row (or, for heading rows, the parent file) and scrolls the
// viewport to the heading when applicable. Directory rows just update
// CurrentTopic so the displayed path follows the cursor.
//
// Topic load is async: the heading scroll is deferred until the background
// load delivers a topicLoadedMsg (see applyTopicLoaded). When the requested
// file is already loaded, the scroll happens immediately and no Cmd is
// returned.
func (m *Model) selectCursor() tea.Cmd {
	node := m.Tree.Cursor()
	if node == nil {
		return nil
	}
	m.CurrentTopic = node

	// A plain directory (no folded index.md) has no content to show — let
	// loadTopic blank the viewport. Directories that fold an index.md flow
	// through the same already-loaded / heading-scroll path as files.
	cn := contentNodeFor(node)
	if cn == nil {
		cmd, _ := m.loadTopic(node)
		return cmd
	}

	alreadyLoaded := m.currentlyLoadedPath == cn.Path && m.currentlyLoadedLocale == m.Locale
	if alreadyLoaded {
		if idx := headingIndex(node); idx >= 0 {
			m.scrollToHeading(idx)
		} else {
			m.Viewport.ScrollToLine(0)
		}
		return nil
	}

	m.pendingHeadingIdx = headingIndex(node)
	cmd, _ := m.loadTopic(node)
	return cmd
}

// scrollToHeading scrolls the viewport so that the heading with the given
// source index appears at the top. The line number is looked up in the
// per-load map built from the injected anchor markers (see
// heading_anchors.go). idx < 0 or out-of-range falls through silently —
// the caller already routes the cursor (it's a navigation no-op).
func (m *Model) scrollToHeading(idx int) {
	if m.Viewport == nil || idx < 0 || idx >= len(m.currentHeadingLines) {
		return
	}
	line := m.currentHeadingLines[idx]
	if line < 0 {
		return
	}
	m.Viewport.ScrollToLine(line)
}

// contentNodeFor returns the docs.Node whose markdown backs a tree row:
// the row's own file node, the folded index.md node for a directory that
// has one, or nil for a plain directory (which renders a blank viewport).
func contentNodeFor(node *TreeNode) *docs.Node {
	if node == nil || node.Node == nil {
		return nil
	}
	if node.Node.IsDir {
		return node.IndexNode // nil when the directory has no index.md
	}
	return node.Node
}

// headingIndex returns the source index of a heading TreeNode (its
// position in the parent file's Headings slice) or -1 when the node is
// not a heading row. Source index matches the index stored in
// m.currentHeadingLines so scrollToHeading can look up the rendered line
// in O(1).
func headingIndex(node *TreeNode) int {
	if node == nil || node.Heading == nil || node.Parent == nil {
		return -1
	}
	for i, sib := range node.Parent.Children {
		if sib == node {
			return i
		}
	}
	return -1
}

// ansiRE matches CSI / OSC escape sequences emitted by glamour so heading
// text can be located in the rendered output.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func (m *Model) ensurePrefetch() {
	if m.Prefetch != nil {
		return
	}
	m.prefetchChan = make(chan ProgressMsg, 10)
	m.Prefetch = NewPrefetch(m.ctx, m.MermaidRenderer, m.prefetchChan)
}

// waitForFileChange returns a Cmd that blocks until the next file-change event.
func waitForFileChange(events <-chan FileChangedMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return nil
		}
		return msg
	}
}

// waitForProgress returns a Cmd that blocks until the next prefetch progress event.
func waitForProgress(ch <-chan ProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}
