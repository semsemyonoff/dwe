package pipeline

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

// subStepStatusKind is the lifecycle state of a parallel sub-step inside the
// bubbletea live view.
type subStepStatusKind int

const (
	subStatusRunning subStepStatusKind = iota
	subStatusDone
	subStatusFailed
	subStatusSkipped
)

// subStepView is the per-sub-step row state rendered by parallelGroupModel.
type subStepView struct {
	addr     string
	idx      int
	total    int
	name     string
	status   subStepStatusKind
	lastLine string
	err      string
	reason   string
}

// Bubbletea messages used to drive parallelGroupModel from PlainReporter.
type (
	subStepOutputMsg struct{ addr, line string }
	subStepDoneMsg   struct {
		addr string
		ok   bool
		err  string
	}
	subStepSkipMsg struct{ addr, reason string }
	groupDoneMsg   struct{}
)

// parallelGroupModel renders one live spinner block for a parallel group.
// Each row mirrors a sub-step; a single shared spinner ticks the running rows.
// The group address is shown as a header.
type parallelGroupModel struct {
	groupAddr string
	subs      []subStepView
	idx       map[string]int // addr → position in subs
	spin      spinner.Model
	done      bool
}

// newParallelGroupModel constructs a model with one row per sub-step. subs is
// retained in declaration order; idx maps sub-step address to slice position.
func newParallelGroupModel(groupAddr string, subs []subStepView) parallelGroupModel {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	idx := make(map[string]int, len(subs))
	for i, s := range subs {
		idx[s.addr] = i
	}
	return parallelGroupModel{
		groupAddr: groupAddr,
		subs:      subs,
		idx:       idx,
		spin:      sp,
	}
}

// Init starts the spinner.
func (m parallelGroupModel) Init() tea.Cmd {
	return m.spin.Tick
}

// Update handles sub-step events and the spinner tick. groupDoneMsg quits.
func (m parallelGroupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case subStepOutputMsg:
		if i, ok := m.idx[msg.addr]; ok {
			m.subs[i].lastLine = msg.line
		}
		return m, nil
	case subStepDoneMsg:
		if i, ok := m.idx[msg.addr]; ok {
			if msg.ok {
				m.subs[i].status = subStatusDone
			} else {
				m.subs[i].status = subStatusFailed
				m.subs[i].err = msg.err
			}
		}
		return m, nil
	case subStepSkipMsg:
		if i, ok := m.idx[msg.addr]; ok {
			m.subs[i].status = subStatusSkipped
			m.subs[i].reason = msg.reason
		}
		return m, nil
	case groupDoneMsg:
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

// View renders one line per sub-step plus a header.
func (m parallelGroupModel) View() tea.View {
	return tea.NewView(m.render())
}

// render is the string-returning form used directly by tests so they don't
// have to instantiate a tea.View.
func (m parallelGroupModel) render() string {
	var b strings.Builder
	header := lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("Parallel group: %s (%d steps)", m.groupAddr, len(m.subs)),
	)
	b.WriteString(header)
	b.WriteByte('\n')
	for _, s := range m.subs {
		b.WriteString("  ")
		b.WriteString(rowIcon(s, m.spin.View()))
		b.WriteByte(' ')
		if s.total > 0 {
			fmt.Fprintf(&b, "[%d/%d] ", s.idx, s.total)
		}
		b.WriteString(s.name)
		switch s.status {
		case subStatusRunning:
			if s.lastLine != "" {
				b.WriteString(": ")
				b.WriteString(truncate(s.lastLine, 80))
			}
		case subStatusFailed:
			if s.err != "" {
				b.WriteString(" — ")
				b.WriteString(s.err)
			}
		case subStatusSkipped:
			if s.reason != "" {
				b.WriteString(" (")
				b.WriteString(s.reason)
				b.WriteString(")")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// rowIcon picks the per-row glyph: spinner frame while running, ✓/✗/◎ once
// the sub-step settles.
func rowIcon(s subStepView, spinFrame string) string {
	switch s.status {
	case subStatusDone:
		return iconDone
	case subStatusFailed:
		return iconFailed
	case subStatusSkipped:
		return iconSkipped
	default:
		return spinFrame
	}
}

// truncate clips s to width runes with an ellipsis tail. Used to keep
// per-row "last line" previews tidy on narrow terminals.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}
