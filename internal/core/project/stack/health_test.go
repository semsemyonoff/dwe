package stack

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/render"
)

// --- AggregateHealth ---

func TestAggregateHealth_AllRunning(t *testing.T) {
	rows := []render.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: true},
		{Name: "second", Enabled: true, Running: true},
	}
	if got := AggregateHealth(rows); got != HealthRunning {
		t.Errorf("AggregateHealth = %d, want HealthRunning (%d)", got, HealthRunning)
	}
}

func TestAggregateHealth_Partial(t *testing.T) {
	rows := []render.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: true},
		{Name: "second", Enabled: true, Running: false},
	}
	if got := AggregateHealth(rows); got != HealthPartial {
		t.Errorf("AggregateHealth = %d, want HealthPartial (%d)", got, HealthPartial)
	}
}

func TestAggregateHealth_NoneRunning(t *testing.T) {
	rows := []render.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: false},
		{Name: "second", Enabled: true, Running: false},
	}
	if got := AggregateHealth(rows); got != HealthStopped {
		t.Errorf("AggregateHealth = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

func TestAggregateHealth_OnlyDisabled(t *testing.T) {
	rows := []render.ServiceTableRow{
		{Name: "second", Mandatory: false, Enabled: false, Running: false},
	}
	if got := AggregateHealth(rows); got != HealthStopped {
		t.Errorf("AggregateHealth = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

func TestAggregateHealth_Empty(t *testing.T) {
	if got := AggregateHealth(nil); got != HealthStopped {
		t.Errorf("AggregateHealth(nil) = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

// --- AggregateHealthFromTopo ---

func TestAggregateHealthFromTopo_AllRunning(t *testing.T) {
	topo := map[string]render.NodeStatus{
		"nginx":    render.NodeRunning,
		"app-main": render.NodeRunning,
		"db":       render.NodeRunning,
		"redis":    render.NodeRunning,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthRunning {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthRunning (%d)", got, HealthRunning)
	}
}

func TestAggregateHealthFromTopo_Partial(t *testing.T) {
	topo := map[string]render.NodeStatus{
		"nginx":    render.NodeRunning,
		"app-main": render.NodeStopped,
		"db":       render.NodeRunning,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthPartial {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthPartial (%d)", got, HealthPartial)
	}
}

func TestAggregateHealthFromTopo_NoneRunning(t *testing.T) {
	topo := map[string]render.NodeStatus{
		"nginx":    render.NodeStopped,
		"app-main": render.NodeStopped,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthStopped {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

func TestAggregateHealthFromTopo_DisabledExcluded(t *testing.T) {
	topo := map[string]render.NodeStatus{
		"nginx":      render.NodeRunning,
		"app-main":   render.NodeRunning,
		"app-second": render.NodeDisabled,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthRunning {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthRunning (%d) (disabled should not count)", got, HealthRunning)
	}
}

func TestAggregateHealthFromTopo_OnlyDisabled(t *testing.T) {
	topo := map[string]render.NodeStatus{
		"app-second": render.NodeDisabled,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthStopped {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

func TestAggregateHealthFromTopo_Empty(t *testing.T) {
	if got := AggregateHealthFromTopo(nil); got != HealthStopped {
		t.Errorf("AggregateHealthFromTopo(nil) = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

// --- HealthFromStatusInput ---

func TestHealthFromStatusInput_TopoTakesPrecedenceOverRows(t *testing.T) {
	// Topo says everything running; service rows say nothing running. Topo wins.
	in := StatusInput{
		Cfg:       nil, // doesn't matter — topo path is taken
		IsRunning: func(string) bool { return false },
		TopoStatus: map[string]render.NodeStatus{
			"nginx":    render.NodeRunning,
			"app-main": render.NodeRunning,
		},
	}
	if got := HealthFromStatusInput(in); got != HealthRunning {
		t.Errorf("HealthFromStatusInput = %d, want HealthRunning (%d)", got, HealthRunning)
	}
}

func TestHealthFromStatusInput_RowsFallbackWhenTopoEmpty(t *testing.T) {
	// Without topology data, the function falls back to row aggregation. Since
	// Cfg is nil here, collectRowsByType returns nil → AggregateHealth → stopped.
	in := StatusInput{
		Cfg:        nil,
		IsRunning:  func(string) bool { return true },
		TopoStatus: nil,
	}
	if got := HealthFromStatusInput(in); got != HealthStopped {
		t.Errorf("HealthFromStatusInput = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

func TestHealthFromStatusInput_RowsFallbackWhenTopoOnlyDisabled(t *testing.T) {
	// A topo map that only contains disabled nodes is NOT runtime data —
	// HasRuntimeStatuses returns false, so the fallback to row aggregation kicks in.
	in := StatusInput{
		Cfg:       nil,
		IsRunning: func(string) bool { return true },
		TopoStatus: map[string]render.NodeStatus{
			"app-second": render.NodeDisabled,
		},
	}
	if got := HealthFromStatusInput(in); got != HealthStopped {
		t.Errorf("HealthFromStatusInput = %d, want HealthStopped (rows fallback when topo is only-disabled), got %d", got, HealthStopped)
	}
}

// --- HasRuntimeStatuses ---

func TestHasRuntimeStatuses_EmptyMap(t *testing.T) {
	if HasRuntimeStatuses(nil) {
		t.Error("expected false for nil map")
	}
	if HasRuntimeStatuses(map[string]render.NodeStatus{}) {
		t.Error("expected false for empty map")
	}
}

func TestHasRuntimeStatuses_OnlyDisabled(t *testing.T) {
	m := map[string]render.NodeStatus{"web": render.NodeDisabled}
	if HasRuntimeStatuses(m) {
		t.Error("expected false when only disabled nodes")
	}
}

func TestHasRuntimeStatuses_WithRunning(t *testing.T) {
	m := map[string]render.NodeStatus{"web": render.NodeRunning}
	if !HasRuntimeStatuses(m) {
		t.Error("expected true when running node exists")
	}
}

func TestHasRuntimeStatuses_WithStopped(t *testing.T) {
	m := map[string]render.NodeStatus{"web": render.NodeStopped}
	if !HasRuntimeStatuses(m) {
		t.Error("expected true when stopped (non-disabled) node exists")
	}
}
