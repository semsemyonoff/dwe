package tui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"devbox-cli/internal/core/docs"
	"devbox-cli/internal/core/docs/mermaid"
	"devbox-cli/internal/core/docs/render"
	"devbox-cli/internal/shared/i18n"
)

type Model struct {
	// TUI state
	Tree       *TreeWidget
	Viewport   *ViewportWidget
	StatusBar  *StatusBarWidget
	Help       help.Model
	Filter     *TreeFilter
	Keys       KeyMap
	Translator i18n.Translator
	Locale     string
	Theme      string
	Title      string // Brand-bar title; "Devbox · <project> · Documentation".

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

	// MmdcNotice is a one-line markdown blockquote prepended to every loaded
	// topic when mmdc is missing on $PATH and the mermaid renderer is not
	// explicitly disabled. Empty means no banner. Populated by runDocsTUI
	// before any topic is loaded, so users see install guidance on the very
	// first frame instead of discovering it via a broken diagram render.
	MmdcNotice string

	// Background rendering
	Prefetch         *Prefetch
	PrefetchProgress ProgressMsg
	prefetchChan     chan ProgressMsg

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

	// Per-session temp dir for "open diagram" exports. One dir per Model so
	// concurrent `devbox docs` sessions don't race on the same temp filename.
	// Lazily created on first export; left on disk at exit (os cleans /tmp).
	diagramExportDir string

	ctx      context.Context
	initCmd  tea.Cmd // cmd to run on first Init() call
	quitting bool
}

type FocusZone int

const (
	FocusTree FocusZone = iota
	FocusViewport
)

var _ tea.Model = (*Model)(nil)

func NewModel(ctx context.Context, roots []docs.DocRoot, locale string, translator i18n.Translator, renderer mermaid.Renderer, termWidth, termHeight int, projectRoot, title, mermaidTheme string) (*Model, error) {
	treeWidget, err := NewTreeWidget(roots, locale)
	if err != nil {
		return nil, err
	}

	theme := resolveMermaidTheme(mermaidTheme)

	hm := help.New()
	hm.ShowAll = true // Always render the full grouped help, matching cmdbrowser.
	applyDocsHelpStyles(&hm.Styles)

	contentW := viewportInnerWidth(termWidth)
	contentH := viewportInnerHeight(termHeight)

	m := &Model{
		Roots:             roots,
		Tree:              treeWidget,
		Locale:            locale,
		Translator:        translator,
		MermaidRenderer:   renderer,
		Help:              hm,
		Filter:            NewTreeFilter(),
		Keys:              DefaultKeyMap(),
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
		TmuxHintShown:     false,
		AvailableLocales:  []string{},
		CurrentSourceLang: "en",
		ProjectRoot:       projectRoot,
		Prefetch:          nil, // Lazily created when needed
		PrefetchProgress:  ProgressMsg{},
		pendingHeadingIdx: -1,
		ctx:               ctx,
		quitting:          false,
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

	if m.Tree.Cursor() != nil {
		m.CurrentTopic = m.Tree.Cursor()
		cmd, _ := m.loadTopic(m.CurrentTopic)
		m.initCmd = cmd
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
	Stale        bool
	Output       string
	Diagrams     []render.DiagramRef
	HeadingLines []int // rendered-line index per source H2/H3 heading
	Err          error
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
	m.StatusBar.SetProgress(0, 0)
	m.loadGen++

	if node == nil || node.Node == nil || node.Node.IsDir {
		m.Viewport.SetContent("")
		m.AvailableLocales = []string{}
		m.CurrentSourceLang = "en"
		m.lastRenderedOutput = ""
		m.lastRenderedDiagrams = nil
		m.currentlyLoadedPath = ""
		m.currentlyLoadedLocale = ""
		m.StatusBar.SetPath("")
		m.pendingHeadingIdx = -1
		return nil, nil
	}

	path := node.Node.Path
	m.AvailableLocales = docs.AvailableLocalesFor(m.Roots, path)

	var sourceRoot docs.DocRoot
	if node.RootName != "" {
		for _, root := range m.Roots {
			if root.Name == node.RootName {
				sourceRoot = root
				break
			}
		}
	}
	if sourceRoot.Name == "" {
		topicPath := strings.TrimSuffix(path, ".md")
		resolved, err := docs.Resolve(m.Roots, topicPath, m.Locale)
		if err != nil {
			m.Viewport.SetContent("Error: " + err.Error())
			return nil, err
		}
		for _, root := range m.Roots {
			if root.Name == resolved.Source {
				sourceRoot = root
				break
			}
		}
	}

	// Snapshot everything the goroutine needs so we never touch m from
	// outside the bubbletea event loop.
	gen := m.loadGen
	locale := m.Locale
	theme := m.Theme
	width := m.ContentWidth
	mmdcNotice := m.MmdcNotice

	return func() tea.Msg {
		content, sourceLang, stale, err := docs.ResolveContent(sourceRoot, path, locale)
		if err != nil {
			return topicLoadedMsg{Generation: gen, Path: path, Locale: locale, Err: err}
		}

		var contentToRender []byte
		switch {
		case sourceLang != locale:
			banner := "> **ℹ Translation not available for `" + locale + "`. Showing English version.**\n\n"
			contentToRender = append([]byte(banner), content...)
		case stale:
			banner := "> **⚠ This translation is outdated (last synced at previous version, current is newer). Press `e` to view the English version.**\n\n"
			contentToRender = append([]byte(banner), content...)
		default:
			contentToRender = content
		}
		if mmdcNotice != "" {
			contentToRender = append([]byte(mmdcNotice), contentToRender...)
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
		return nil
	}

	m.CurrentSourceLang = msg.SourceLang
	m.lastRenderedOutput = msg.Output
	m.lastRenderedDiagrams = msg.Diagrams
	m.currentHeadingLines = msg.HeadingLines
	m.Viewport.SetContent(m.inlineDiagrams(msg.Output, msg.Diagrams))
	m.StatusBar.SetPath(msg.Path)
	m.StatusBar.SetLanguage(msg.SourceLang)
	m.currentlyLoadedPath = msg.Path
	m.currentlyLoadedLocale = msg.Locale
	m.DiagramState = NewDiagramState(msg.Diagrams)

	if m.pendingHeadingIdx >= 0 {
		m.scrollToHeading(m.pendingHeadingIdx)
		m.pendingHeadingIdx = -1
	} else {
		m.Viewport.ScrollToLine(0)
	}

	if len(msg.Diagrams) == 0 {
		return nil
	}
	m.ensurePrefetch()
	items := make([]WorkItem, len(msg.Diagrams))
	for i, diag := range msg.Diagrams {
		theme := mermaid.ThemeDark
		if m.Theme == "light" {
			theme = mermaid.ThemeLight
		}
		items[i] = WorkItem{
			Source: diag.Source,
			Theme:  theme,
			Width:  diagramRenderWidth(),
			Index:  i,
		}
	}
	m.Prefetch.Queue(items)
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

// quit closes background services and returns the tea.Quit command.
func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if m.Prefetch != nil {
		m.Prefetch.Close()
		if m.prefetchChan != nil {
			close(m.prefetchChan)
			m.prefetchChan = nil
		}
	}
	if m.Watcher != nil {
		_ = m.Watcher.Close()
	}
	return m, tea.Quit
}

// handleFilterKey routes input while the tree filter is active. Esc closes,
// Enter accepts (keeps cursor where it is, drops the filter), Backspace pops
// a rune, Up/Down navigate the filtered list, and any printable rune
// extends the query and re-filters live.
func (m *Model) handleFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.Filter.Close()
		m.Tree.ApplyFilter(nil)
		return m, nil
	case "enter":
		// Capture the highlighted match BEFORE clearing the filter.
		// ApplyFilter(nil) recomputes unfiltered visibility, and
		// ensureCursorVisible would reset the cursor to visible[0] if the
		// match was a heading or sat under a collapsed parent — leaving
		// the user on the wrong topic. By expanding ancestors first we
		// guarantee the captured node stays visible after the filter
		// drops, and then re-pin the cursor on it before loading.
		picked := m.Tree.Cursor()
		expandAncestors(picked)
		m.Filter.Close()
		m.Tree.ApplyFilter(nil)
		if picked != nil {
			m.Tree.SetCursor(picked)
		}
		cmd := m.selectCursor()
		m.FocusZone = FocusViewport
		return m, cmd
	case "backspace":
		m.Filter.Backspace()
		m.Tree.ApplyFilter(m.Filter)
		// Typing doesn't trigger a content load: when the cursor lands on
		// a directory mid-query the viewport would blank, and even on a
		// file the per-keystroke async glamour run is wasted work. The
		// user previews matches with Up/Down or commits with Enter.
		return m, nil
	case "up":
		m.Tree.MoveUp()
		return m, m.selectCursor()
	case "down":
		m.Tree.MoveDown()
		return m, m.selectCursor()
	}
	// Printable runes extend the query. Reject control chars and multi-rune
	// keys (function keys, arrows already handled above) so the query stays
	// to typed text.
	if len(msg.Text) == 0 {
		return m, nil
	}
	for _, r := range msg.Text {
		if r >= 32 && r != 127 {
			m.Filter.Append(r)
		}
	}
	m.Tree.ApplyFilter(m.Filter)
	// See backspace branch above: typing only refilters the tree; content
	// preview is explicit (Up/Down) or commits (Enter).
	return m, nil
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
	if m.Tree.IsDir(node) {
		cmd, _ := m.loadTopic(node)
		return cmd
	}

	alreadyLoaded := m.currentlyLoadedPath == node.Node.Path && m.currentlyLoadedLocale == m.Locale
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

func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.initCmd != nil {
		cmds = append(cmds, m.initCmd)
		m.initCmd = nil
	}
	if m.Watcher != nil {
		cmds = append(cmds, waitForFileChange(m.Watcher.Events()))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.TermWidth = msg.Width
		m.TermHeight = msg.Height
		m.ContentWidth = viewportInnerWidth(m.TermWidth)
		m.ContentHeight = viewportInnerHeight(m.TermHeight)
		if m.Viewport != nil {
			m.Viewport.SetDimensions(m.ContentWidth, m.ContentHeight)
		}
		return m, nil
	case topicLoadedMsg:
		return m, m.applyTopicLoaded(msg)
	case FileChangedMsg:
		// File changed on disk; reload the current topic if it matches.
		// msg.Path is an absolute fsnotify path; Node.Path is relative to the docs root,
		// so we reconstruct the absolute path from ProjectRoot for comparison.
		var topicCmd tea.Cmd
		if m.CurrentTopic != nil && m.CurrentTopic.Node != nil && m.ProjectRoot != "" {
			projectDocsPath := filepath.Join(m.ProjectRoot, "docs")
			absPath := filepath.Join(projectDocsPath, m.CurrentTopic.Node.Path)
			if filepath.Clean(absPath) == filepath.Clean(msg.Path) {
				topicCmd, _ = m.loadTopic(m.CurrentTopic)
			}
		}
		// Re-subscribe so the next event is delivered.
		if m.Watcher != nil {
			return m, tea.Batch(topicCmd, waitForFileChange(m.Watcher.Events()))
		}
		return m, topicCmd
	case ProgressMsg:
		// Drop messages from a previous topic — when the user navigates
		// away while workers are still rendering, ticks for the prior
		// topic can still arrive on the persistent channel. Comparing the
		// message generation against the prefetch's current generation
		// keeps the model state aligned with the topic actually loaded.
		if m.Prefetch != nil && msg.Generation != m.Prefetch.Generation() {
			if m.prefetchChan != nil {
				return m, waitForProgress(m.prefetchChan)
			}
			return m, nil
		}
		// Each prefetch tick may have populated the cache for the diagrams
		// of the currently-loaded topic, so re-run inline substitution on
		// the cached glamour output. Swaps "<📊 [rendering…]>" for either
		// the cached-state text or the "render failed" fallback.
		m.PrefetchProgress = msg
		m.StatusBar.SetProgress(msg.Rendered, msg.Total)
		if m.lastRenderedOutput != "" && len(m.lastRenderedDiagrams) > 0 {
			m.Viewport.SetContent(m.inlineDiagrams(m.lastRenderedOutput, m.lastRenderedDiagrams))
		}
		if m.prefetchChan != nil {
			return m, waitForProgress(m.prefetchChan)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Ctrl-C always quits, even while filtering, so the user has a guaranteed
	// way out without first having to close the filter.
	if msg.String() == "ctrl+c" {
		return m.quit()
	}

	// Filter mode captures most keys. Routed early so `q` and other
	// single-letter shortcuts type into the query instead of triggering
	// commands.
	if m.Filter != nil && m.Filter.Active {
		return m.handleFilterKey(msg)
	}

	if key.Matches(msg, m.Keys.Quit) {
		return m.quit()
	}

	// Tree filter: "/" opens an inline filter on tree row labels. The
	// previous "content search" stub never had an entry UI so the key is
	// repurposed here.
	if key.Matches(msg, m.Keys.SearchStart) {
		m.Filter.Open()
		m.Tree.ApplyFilter(m.Filter)
		return m, nil
	}

	// Handle diagram keys
	if key.Matches(msg, m.Keys.DiagramNext) {
		m.DiagramState.Next()
		m.refreshDiagramView()
		return m, nil
	}
	if key.Matches(msg, m.Keys.DiagramPrev) {
		m.DiagramState.Prev()
		m.refreshDiagramView()
		return m, nil
	}
	if key.Matches(msg, m.Keys.DiagramOpen) {
		_ = m.openCurrentDiagram()
		return m, nil
	}
	if key.Matches(msg, m.Keys.DiagramCopy) {
		if diagram := m.DiagramState.CurrentDiagram(); diagram != nil {
			_ = CopyViaOSC52(diagram.Source, os.Stdout)
			if !m.TmuxHintShown && ClipboardTmuxHint() {
				m.TmuxHintShown = true
			}
		}
		return m, nil
	}

	// Handle language cycling
	if key.Matches(msg, m.Keys.LanguageCycle) {
		if m.CurrentTopic != nil && m.CurrentTopic.Node != nil && len(m.AvailableLocales) > 0 {
			currentIdx := -1
			for i, l := range m.AvailableLocales {
				if l == m.Locale {
					currentIdx = i
					break
				}
			}
			nextIdx := (currentIdx + 1) % len(m.AvailableLocales)
			m.Locale = m.AvailableLocales[nextIdx]
			return m, m.applyLocaleChange()
		}
		return m, nil
	}

	// Handle show English (only if current file is translated)
	if key.Matches(msg, m.Keys.ShowEnglish) {
		if m.CurrentTopic != nil && m.CurrentTopic.Node != nil && m.CurrentSourceLang != "en" {
			m.Locale = "en"
			return m, m.applyLocaleChange()
		}
		return m, nil
	}

	// Handle reload
	if key.Matches(msg, m.Keys.Reload) {
		if m.CurrentTopic != nil && m.CurrentTopic.Node != nil {
			cmd, _ := m.loadTopic(m.CurrentTopic)
			return m, cmd
		}
		return m, nil
	}

	var cmd tea.Cmd
	switch {
	case key.Matches(msg, m.Keys.Up):
		if m.FocusZone == FocusTree {
			m.Tree.MoveUp()
			cmd = m.selectCursor()
		} else {
			m.Viewport.ScrollUp()
		}
	case key.Matches(msg, m.Keys.Down):
		if m.FocusZone == FocusTree {
			m.Tree.MoveDown()
			cmd = m.selectCursor()
		} else {
			m.Viewport.ScrollDown()
		}
	case key.Matches(msg, m.Keys.Left):
		if m.FocusZone == FocusTree {
			m.Tree.Toggle()
		}
	case key.Matches(msg, m.Keys.Right):
		if m.FocusZone == FocusTree {
			m.Tree.Toggle()
		}
	case key.Matches(msg, m.Keys.Start):
		if m.FocusZone == FocusTree {
			m.Tree.MoveStart()
			cmd = m.selectCursor()
		} else {
			m.Viewport.ScrollStart()
		}
	case key.Matches(msg, m.Keys.End):
		if m.FocusZone == FocusTree {
			m.Tree.MoveEnd()
			cmd = m.selectCursor()
		} else {
			m.Viewport.ScrollEnd()
		}
	case key.Matches(msg, m.Keys.Tab):
		if m.FocusZone == FocusTree {
			m.FocusZone = FocusViewport
		} else {
			m.FocusZone = FocusTree
		}
	case key.Matches(msg, m.Keys.Enter):
		if m.FocusZone == FocusTree && m.Tree.Cursor() != nil {
			node := m.Tree.Cursor()
			switch {
			case m.Tree.IsDir(node) || (node.Heading == nil && len(node.Children) > 0):
				// Directories and files-with-headings expand on Enter; focus
				// moves to the viewport so the user can read what they
				// expanded without an extra Tab keypress.
				if !node.Expanded {
					m.Tree.Toggle()
				}
				m.FocusZone = FocusViewport
			default:
				cmd = m.selectCursor()
				m.FocusZone = FocusViewport
			}
		}
	}

	return m, cmd
}

func (m *Model) View() tea.View {
	return m.renderView()
}

func (m *Model) renderView() tea.View {
	return m.renderTwoPanel()
}
