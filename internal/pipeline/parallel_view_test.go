package pipeline

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// helper: build a model with N running sub-steps.
func newTestModel(addrs ...string) parallelGroupModel {
	views := make([]subStepView, len(addrs))
	for i, a := range addrs {
		views[i] = subStepView{
			addr:   a,
			idx:    i + 1,
			total:  len(addrs),
			name:   a,
			status: subStatusRunning,
		}
	}
	return newParallelGroupModel("phase/group", views)
}

func TestParallelGroupModel_Update_OutputThenDone(t *testing.T) {
	m := newTestModel("a", "b")
	var tm tea.Model = m
	tm, _ = tm.Update(subStepOutputMsg{addr: "a", line: "hello"})
	tm, _ = tm.Update(subStepDoneMsg{addr: "a", ok: true})
	got := tm.(parallelGroupModel)
	if got.subs[0].status != subStatusDone {
		t.Fatalf("expected sub a status=done, got %v", got.subs[0].status)
	}
	// After done, lastLine should remain visible in state but the row's
	// rendered glyph switches to ✓.
	v := got.render()
	if !strings.Contains(v, iconDone) {
		t.Fatalf("expected done icon in view, got:\n%s", v)
	}
}

func TestParallelGroupModel_Update_Skip(t *testing.T) {
	m := newTestModel("a")
	var tm tea.Model = m
	tm, _ = tm.Update(subStepSkipMsg{addr: "a", reason: "when=false"})
	got := tm.(parallelGroupModel)
	if got.subs[0].status != subStatusSkipped {
		t.Fatalf("expected skipped status")
	}
	if got.subs[0].reason != "when=false" {
		t.Fatalf("expected reason captured")
	}
	v := got.render()
	if !strings.Contains(v, iconSkipped) || !strings.Contains(v, "when=false") {
		t.Fatalf("expected skip icon + reason in view, got:\n%s", v)
	}
}

func TestParallelGroupModel_Update_Fail(t *testing.T) {
	m := newTestModel("a")
	var tm tea.Model = m
	tm, _ = tm.Update(subStepDoneMsg{addr: "a", ok: false, err: "boom"})
	got := tm.(parallelGroupModel)
	if got.subs[0].status != subStatusFailed {
		t.Fatalf("expected failed status")
	}
	v := got.render()
	if !strings.Contains(v, iconFailed) || !strings.Contains(v, "boom") {
		t.Fatalf("expected fail icon + err in view, got:\n%s", v)
	}
}

func TestParallelGroupModel_Update_GroupDoneQuits(t *testing.T) {
	m := newTestModel("a")
	var tm tea.Model = m
	tm, cmd := tm.Update(groupDoneMsg{})
	got := tm.(parallelGroupModel)
	if !got.done {
		t.Fatalf("expected done=true after groupDoneMsg")
	}
	if cmd == nil {
		t.Fatalf("expected non-nil cmd (tea.Quit) after groupDoneMsg")
	}
	// tea.Quit is a function returning a QuitMsg; sanity-check that.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestParallelGroupModel_View_NonEmpty(t *testing.T) {
	m := newTestModel("a", "b", "c")
	v := m.render()
	if v == "" {
		t.Fatal("expected non-empty view")
	}
	for _, want := range []string{"Parallel group:", "phase/group", "a", "b", "c"} {
		if !strings.Contains(v, want) {
			t.Errorf("expected %q in view, got:\n%s", want, v)
		}
	}
}

func TestParallelGroupModel_UnknownAddrIgnored(t *testing.T) {
	m := newTestModel("a")
	var tm tea.Model = m
	// Unknown addresses must be silently ignored, not panic.
	tm, _ = tm.Update(subStepOutputMsg{addr: "unknown", line: "x"})
	tm, _ = tm.Update(subStepDoneMsg{addr: "unknown", ok: true})
	tm, _ = tm.Update(subStepSkipMsg{addr: "unknown"})
	got := tm.(parallelGroupModel)
	if got.subs[0].status != subStatusRunning {
		t.Fatalf("known sub-step state must not be perturbed by unknown messages")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"hello", 0, ""},
		{"héllo", 3, "hé…"},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.width); got != c.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}
