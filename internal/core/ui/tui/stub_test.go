package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// stubResult is the typed result the stub plugin returns through Result(); the
// plugin_test asserts Run-style callers get it back unchanged.
type stubResult struct {
	Selected string
}

// stubMsg is an arbitrary plugin message used by the async-preservation tests to
// prove a non-key message forwarded through Frame.Update reaches the plugin.
type stubMsg struct{ payload string }

// stub action ids and a sentinel command the stub returns from HandleAction so a
// test can confirm the command flows back to the framework.
const (
	stubActionOpen   Action = "stub.open"
	stubActionReload Action = "stub.reload"
)

func stubCmd() tea.Msg { return stubMsg{payload: "from-action"} }

// stubPlugin is a minimal but complete [Plugin] implementation. It is the
// contract proof reused by every golden/unit test from Task 3 onward: it declares
// two weighted panels, registers a couple of actions, records lifecycle calls and
// every Update message (for async preservation), exposes a fixed status context,
// and returns a typed result.
type stubPlugin struct {
	// recorded observations.
	initCalled    bool
	closeCalled   bool
	closeErr      error
	lastResize    Region
	gotMsgs       []tea.Msg
	handledAction Action

	// pending overlay the plugin will surface on the next PendingOverlay drain.
	pending   *Overlay
	statusCtx string
	result    stubResult

	// capturing toggles CapturingInput() so the no-overlay capture branch can be
	// exercised. Default false (normal registry dispatch).
	capturing bool

	// lifecycle-order guard: records the order of Init/Update/Close calls so a
	// test can assert Init precedes any Update and Close comes last.
	callOrder []string
}

const (
	stubPanelLeft  PanelID = "left"
	stubPanelRight PanelID = "right"
)

func newStubPlugin() *stubPlugin {
	return &stubPlugin{
		statusCtx: "stub: ready",
		result:    stubResult{Selected: "none"},
	}
}

func (p *stubPlugin) Init() tea.Cmd {
	p.initCalled = true
	p.callOrder = append(p.callOrder, "init")
	return nil
}

func (p *stubPlugin) Close() error {
	p.closeCalled = true
	p.callOrder = append(p.callOrder, "close")
	return p.closeErr
}

func (p *stubPlugin) Resize(body Region) { p.lastResize = body }

func (p *stubPlugin) Update(msg tea.Msg) tea.Cmd {
	p.gotMsgs = append(p.gotMsgs, msg)
	p.callOrder = append(p.callOrder, "update")
	return nil
}

func (p *stubPlugin) ViewPanel(id PanelID, inner Region) string {
	return fmt.Sprintf("[%s %dx%d]", id, inner.Width, inner.Height)
}

func (p *stubPlugin) Panels() []Panel {
	return []Panel{
		{ID: stubPanelLeft, Title: "Left", Weight: 1},
		{ID: stubPanelRight, Title: "Right", Weight: 2},
	}
}

func (p *stubPlugin) StatusContext() string { return p.statusCtx }

func (p *stubPlugin) Actions(reg *Registry) error {
	if err := reg.Register(stubActionOpen, Binding{Keys: []string{"o"}, Desc: "Open item", Section: "Stub"}); err != nil {
		return err
	}
	return reg.Register(stubActionReload, Binding{Keys: []string{"r"}, Desc: "Reload", Section: "Stub"})
}

func (p *stubPlugin) HandleAction(a Action) (tea.Cmd, bool) {
	switch a {
	case stubActionOpen:
		p.handledAction = a
		return stubCmd, true
	case stubActionReload:
		p.handledAction = a
		return nil, true
	default:
		return nil, false
	}
}

func (p *stubPlugin) PendingOverlay() (Overlay, bool) {
	if p.pending == nil {
		return Overlay{}, false
	}
	ov := *p.pending
	p.pending = nil
	return ov, true
}

func (p *stubPlugin) Result() any { return p.result }

func (p *stubPlugin) CapturingInput() bool { return p.capturing }
