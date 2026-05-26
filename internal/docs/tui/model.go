package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"devbox-cli/internal/docs"
	"devbox-cli/internal/docs/mermaid"
	"devbox-cli/internal/docs/render"
	"devbox-cli/internal/i18n"
)

type Model struct {
	// TUI state
	Tree       *TreeWidget
	Viewport   *ViewportWidget
	StatusBar  *StatusBarWidget
	Keys       KeyMap
	Translator i18n.Translator
	Locale     string
	Theme      string

	// Data
	Roots []docs.DocRoot

	// Rendering
	MermaidRenderer mermaid.Renderer
	CanInlineImages bool

	// Current state
	CurrentTopic      *TreeNode
	FocusZone         FocusZone
	ContentWidth      int
	ContentHeight     int
	TermWidth         int
	TermHeight        int
	SearchState       *SearchState
	DiagramState      *DiagramState
	SearchIndex       *SearchIndex
	TmuxHintShown     bool
	AvailableLocales  []string // Available languages for the current file
	CurrentSourceLang string   // The actual source language (en if fallback, otherwise requested)
	Watcher           *Watcher // File change watcher (project docs only)
	ProjectRoot       string   // Path to the project root

	// Background rendering
	Prefetch         *Prefetch
	PrefetchProgress ProgressMsg
	prefetchChan     chan ProgressMsg

	quitting bool
}

type FocusZone int

const (
	FocusTree FocusZone = iota
	FocusViewport
)

var _ tea.Model = (*Model)(nil)

func NewModel(ctx context.Context, roots []docs.DocRoot, locale string, translator i18n.Translator, renderer mermaid.Renderer, termWidth, termHeight int, projectRoot string) (*Model, error) {
	treeWidget, err := NewTreeWidget(roots)
	if err != nil {
		return nil, err
	}

	darkBg := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	theme := "dark"
	if !darkBg {
		theme = "light"
	}

	m := &Model{
		Roots:             roots,
		Tree:              treeWidget,
		Locale:            locale,
		Translator:        translator,
		MermaidRenderer:   renderer,
		CanInlineImages:   mermaid.CanInline(),
		Keys:              DefaultKeyMap(),
		Theme:             theme,
		FocusZone:         FocusTree,
		TermWidth:         termWidth,
		TermHeight:        termHeight,
		ContentWidth:      termWidth - 40,
		ContentHeight:     termHeight - 4,
		Viewport:          NewViewportWidget(termWidth-40, termHeight-4),
		StatusBar:         NewStatusBarWidget(),
		SearchState:       NewSearchState(),
		DiagramState:      NewDiagramState(nil),
		SearchIndex:       BuildSearchIndex(nil, ""),
		TmuxHintShown:     false,
		AvailableLocales:  []string{},
		CurrentSourceLang: "en",
		ProjectRoot:       projectRoot,
		Prefetch:          nil, // Lazily created when needed
		PrefetchProgress:  ProgressMsg{},
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
		_, _ = m.loadTopic(m.CurrentTopic)
	}

	return m, nil
}

// loadTopic loads and renders content for the given tree node.
// It returns a tea.Cmd that starts listening for prefetch progress if diagrams were queued.
func (m *Model) loadTopic(node *TreeNode) (tea.Cmd, error) {
	if node == nil || node.Node == nil || node.Node.IsDir {
		m.Viewport.SetContent("")
		m.AvailableLocales = []string{}
		m.CurrentSourceLang = "en"
		return nil, nil
	}

	path := node.Node.Path

	// Get available locales for this file
	m.AvailableLocales = docs.AvailableLocalesFor(m.Roots, path)

	resolved, err := docs.Resolve(m.Roots, path, m.Locale)
	if err != nil {
		m.Viewport.SetContent("Error: " + err.Error())
		return nil, err
	}

	// Find the DocRoot that matches the resolved source
	var sourceRoot docs.DocRoot
	for _, root := range m.Roots {
		if root.Name == resolved.Source {
			sourceRoot = root
			break
		}
	}

	content, sourceLang, stale, err := docs.ResolveContent(sourceRoot, path, m.Locale)
	if err != nil {
		m.Viewport.SetContent("Error: " + err.Error())
		return nil, err
	}

	m.CurrentSourceLang = sourceLang

	var banners []string
	// Missing translation: different source language than requested
	if sourceLang != m.Locale {
		banners = append(banners, "> **ℹ Translation not available for `"+m.Locale+"`. Showing English version.**")
	}
	// Stale translation: content hash mismatch
	if stale {
		banners = append(banners, "> **⚠ This translation is outdated (last synced at previous version, current is newer). Press `e` to view the English version.**")
	}

	placeholderFunc := func(index int) render.MermaidPlaceholder {
		return render.MermaidPlaceholder{
			Text: "<📊 [rendering]>",
		}
	}

	opts := render.Opts{
		Theme:           m.Theme,
		Width:           m.ContentWidth,
		MermaidRenderer: m.MermaidRenderer,
		CanInline:       m.CanInlineImages,
	}

	result, err := render.Render(content, opts, placeholderFunc)
	if err != nil {
		m.Viewport.SetContent("Error rendering: " + err.Error())
		return nil, err
	}

	var output string
	if len(banners) > 0 {
		var sb strings.Builder
		for _, banner := range banners {
			sb.WriteString(banner)
			sb.WriteString("\n\n")
		}
		sb.Write(result.Output)
		output = sb.String()
	} else {
		output = string(result.Output)
	}

	m.Viewport.SetContent(output)
	m.StatusBar.SetPath(path)
	m.StatusBar.SetLanguage(sourceLang)

	// Update search index and diagram state for the new content
	m.SearchIndex = BuildSearchIndex(content, path)
	m.DiagramState = NewDiagramState(result.Diagrams)
	m.SearchState.Close()

	// Queue diagrams for prefetch rendering if we have any; return a Cmd to start
	// listening for progress so the status bar updates as diagrams are rendered.
	if len(result.Diagrams) > 0 {
		m.ensurePrefetch()
		items := make([]WorkItem, len(result.Diagrams))
		for i, diag := range result.Diagrams {
			theme := mermaid.ThemeDark
			if m.Theme == "light" {
				theme = mermaid.ThemeLight
			}
			items[i] = WorkItem{
				Source: diag.Source,
				Theme:  theme,
				Width:  m.ContentWidth,
				Index:  i,
			}
		}
		m.Prefetch.Queue(items)
		return waitForProgress(m.prefetchChan), nil
	}

	return nil, nil
}

func (m *Model) ensurePrefetch() {
	if m.Prefetch != nil {
		return
	}
	ctx := context.Background()
	m.prefetchChan = make(chan ProgressMsg, 10)
	m.Prefetch = NewPrefetch(ctx, m.MermaidRenderer, m.prefetchChan)
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
	if m.Watcher != nil {
		return waitForFileChange(m.Watcher.Events())
	}
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.TermWidth = msg.Width
		m.TermHeight = msg.Height
		m.ContentWidth = m.TermWidth - 40
		m.ContentHeight = m.TermHeight - 4
		if m.Viewport != nil {
			m.Viewport.SetDimensions(m.ContentWidth, m.ContentHeight)
		}
		return m, nil
	case FileChangedMsg:
		// File changed on disk; reload the current topic if it matches.
		var topicCmd tea.Cmd
		if m.CurrentTopic != nil && m.CurrentTopic.Node != nil && m.CurrentTopic.Node.Path == msg.Path {
			topicCmd, _ = m.loadTopic(m.CurrentTopic)
		}
		// Re-subscribe so the next event is delivered.
		if m.Watcher != nil {
			return m, tea.Batch(topicCmd, waitForFileChange(m.Watcher.Events()))
		}
		return m, topicCmd
	case ProgressMsg:
		// Update progress from prefetch worker pool; re-subscribe for the next tick.
		m.PrefetchProgress = msg
		if m.prefetchChan != nil {
			return m, waitForProgress(m.prefetchChan)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.Keys.Quit) {
		m.quitting = true
		// Clean up background services before quitting
		if m.Prefetch != nil {
			m.Prefetch.Close()
		}
		if m.Watcher != nil {
			_ = m.Watcher.Close()
		}
		return m, tea.Quit
	}

	// Handle search keys
	if key.Matches(msg, m.Keys.SearchStart) {
		m.SearchState.Open("", m.SearchIndex)
		return m, nil
	}
	if key.Matches(msg, m.Keys.SearchNext) && m.SearchState.IsOpen {
		m.SearchState.Next()
		return m, nil
	}
	if key.Matches(msg, m.Keys.SearchPrev) && m.SearchState.IsOpen {
		m.SearchState.Prev()
		return m, nil
	}

	// Handle diagram keys
	if key.Matches(msg, m.Keys.DiagramNext) {
		m.DiagramState.Next()
		return m, nil
	}
	if key.Matches(msg, m.Keys.DiagramPrev) {
		m.DiagramState.Prev()
		return m, nil
	}
	if key.Matches(msg, m.Keys.DiagramCopy) {
		if diagram := m.DiagramState.CurrentDiagram(); diagram != nil {
			_ = CopyViaOSC52(diagram.Source, os.Stdout)
			if !m.TmuxHintShown && ClipboardTmuxHint() {
				m.TmuxHintShown = true
				// In a real implementation, we'd update the status bar with the hint
				// For now, just mark it as shown
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
			cmd, _ := m.loadTopic(m.CurrentTopic)
			return m, cmd
		}
		return m, nil
	}

	// Handle show English (only if current file is translated)
	if key.Matches(msg, m.Keys.ShowEnglish) {
		if m.CurrentTopic != nil && m.CurrentTopic.Node != nil && m.CurrentSourceLang != "en" {
			m.Locale = "en"
			cmd, _ := m.loadTopic(m.CurrentTopic)
			return m, cmd
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
			if m.Tree.Cursor() != nil {
				m.CurrentTopic = m.Tree.Cursor()
				cmd, _ = m.loadTopic(m.CurrentTopic)
			}
		} else {
			m.Viewport.ScrollUp()
		}
	case key.Matches(msg, m.Keys.Down):
		if m.FocusZone == FocusTree {
			m.Tree.MoveDown()
			if m.Tree.Cursor() != nil {
				m.CurrentTopic = m.Tree.Cursor()
				cmd, _ = m.loadTopic(m.CurrentTopic)
			}
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
			if m.Tree.Cursor() != nil {
				m.CurrentTopic = m.Tree.Cursor()
				cmd, _ = m.loadTopic(m.CurrentTopic)
			}
		} else {
			m.Viewport.ScrollStart()
		}
	case key.Matches(msg, m.Keys.End):
		if m.FocusZone == FocusTree {
			m.Tree.MoveEnd()
			if m.Tree.Cursor() != nil {
				m.CurrentTopic = m.Tree.Cursor()
				cmd, _ = m.loadTopic(m.CurrentTopic)
			}
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
		if m.FocusZone == FocusTree && m.Tree.Cursor() != nil && !m.Tree.IsDir(m.Tree.Cursor()) {
			m.CurrentTopic = m.Tree.Cursor()
			cmd, _ = m.loadTopic(m.CurrentTopic)
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
