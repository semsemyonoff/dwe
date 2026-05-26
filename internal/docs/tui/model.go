package tui

import (
	"os"
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
	CurrentTopic  *TreeNode
	FocusZone     FocusZone
	ContentWidth  int
	ContentHeight int
	TermWidth     int
	TermHeight    int

	quitting bool
}

type FocusZone int

const (
	FocusTree FocusZone = iota
	FocusViewport
)

var _ tea.Model = (*Model)(nil)

func NewModel(roots []docs.DocRoot, locale string, translator i18n.Translator, renderer mermaid.Renderer, termWidth, termHeight int) (*Model, error) {
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
		Roots:              roots,
		Tree:               treeWidget,
		Locale:             locale,
		Translator:         translator,
		MermaidRenderer:    renderer,
		CanInlineImages:    mermaid.CanInline(),
		Keys:               DefaultKeyMap(),
		Theme:              theme,
		FocusZone:          FocusTree,
		TermWidth:          termWidth,
		TermHeight:         termHeight,
		ContentWidth:       termWidth - 40,
		ContentHeight:      termHeight - 4,
		Viewport:           NewViewportWidget(termWidth - 40, termHeight - 4),
		StatusBar:          NewStatusBarWidget(),
		quitting:           false,
	}

	if m.Tree.Cursor() != nil {
		m.CurrentTopic = m.Tree.Cursor()
		if err := m.loadTopic(m.CurrentTopic); err != nil {
			// Continue even if loading fails
		}
	}

	return m, nil
}

func (m *Model) loadTopic(node *TreeNode) error {
	if node == nil || node.Node == nil || node.Node.IsDir {
		m.Viewport.SetContent("")
		return nil
	}

	path := node.Node.Path

	resolved, err := docs.Resolve(m.Roots, path, m.Locale)
	if err != nil {
		m.Viewport.SetContent("Error: " + err.Error())
		return err
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
		return err
	}

	var banners []string
	if sourceLang != m.Locale && m.Locale != "en" {
		banners = append(banners, "> **Note:** This file is not translated to '"+m.Locale+"'. Showing English version.")
	}
	if stale {
		banners = append(banners, "> **Note:** This translation is outdated.")
	}

	placeholderFunc := func(index int) render.MermaidPlaceholder {
		return render.MermaidPlaceholder{
			Text: "<📊 [rendering]>",
		}
	}

	opts := render.RenderOpts{
		Theme:             m.Theme,
		Width:             m.ContentWidth,
		MermaidRenderer:   m.MermaidRenderer,
		CanInline:         m.CanInlineImages,
	}

	result, err := render.Render(content, opts, placeholderFunc)
	if err != nil {
		m.Viewport.SetContent("Error rendering: " + err.Error())
		return err
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

	return nil
}

func (m *Model) Init() tea.Cmd {
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
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.Keys.Quit) {
		m.quitting = true
		return m, tea.Quit
	}

	switch {
	case key.Matches(msg, m.Keys.Up):
		if m.FocusZone == FocusTree {
			m.Tree.MoveUp()
			if m.Tree.Cursor() != nil {
				m.CurrentTopic = m.Tree.Cursor()
				_ = m.loadTopic(m.CurrentTopic)
			}
		} else {
			m.Viewport.ScrollUp()
		}
	case key.Matches(msg, m.Keys.Down):
		if m.FocusZone == FocusTree {
			m.Tree.MoveDown()
			if m.Tree.Cursor() != nil {
				m.CurrentTopic = m.Tree.Cursor()
				_ = m.loadTopic(m.CurrentTopic)
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
				_ = m.loadTopic(m.CurrentTopic)
			}
		} else {
			m.Viewport.ScrollStart()
		}
	case key.Matches(msg, m.Keys.End):
		if m.FocusZone == FocusTree {
			m.Tree.MoveEnd()
			if m.Tree.Cursor() != nil {
				m.CurrentTopic = m.Tree.Cursor()
				_ = m.loadTopic(m.CurrentTopic)
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
			_ = m.loadTopic(m.CurrentTopic)
		}
	}

	return m, nil
}

func (m *Model) View() tea.View {
	return m.renderView()
}

func (m *Model) renderView() tea.View {
	// Placeholder implementation for now
	content := "docs TUI\n"
	if m.CurrentTopic != nil && m.CurrentTopic.Node != nil {
		content = m.CurrentTopic.Node.Path + "\n"
	}
	content += m.Viewport.View()

	v := tea.NewView(content)
	return v
}
