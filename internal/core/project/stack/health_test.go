package stack

import (
	"testing"

	"devbox-cli/internal/core/ui"
)

// --- AggregateHealth ---

func TestAggregateHealth_AllRunning(t *testing.T) {
	rows := []ui.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: true},
		{Name: "second", Enabled: true, Running: true},
	}
	if got := AggregateHealth(rows); got != HealthRunning {
		t.Errorf("AggregateHealth = %d, want HealthRunning (%d)", got, HealthRunning)
	}
}

func TestAggregateHealth_Partial(t *testing.T) {
	rows := []ui.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: true},
		{Name: "second", Enabled: true, Running: false},
	}
	if got := AggregateHealth(rows); got != HealthPartial {
		t.Errorf("AggregateHealth = %d, want HealthPartial (%d)", got, HealthPartial)
	}
}

func TestAggregateHealth_NoneRunning(t *testing.T) {
	rows := []ui.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: false},
		{Name: "second", Enabled: true, Running: false},
	}
	if got := AggregateHealth(rows); got != HealthStopped {
		t.Errorf("AggregateHealth = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

func TestAggregateHealth_OnlyDisabled(t *testing.T) {
	rows := []ui.ServiceTableRow{
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
	topo := map[string]ui.NodeStatus{
		"nginx":    ui.NodeRunning,
		"app-main": ui.NodeRunning,
		"db":       ui.NodeRunning,
		"redis":    ui.NodeRunning,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthRunning {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthRunning (%d)", got, HealthRunning)
	}
}

func TestAggregateHealthFromTopo_Partial(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"nginx":    ui.NodeRunning,
		"app-main": ui.NodeStopped,
		"db":       ui.NodeRunning,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthPartial {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthPartial (%d)", got, HealthPartial)
	}
}

func TestAggregateHealthFromTopo_NoneRunning(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"nginx":    ui.NodeStopped,
		"app-main": ui.NodeStopped,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthStopped {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthStopped (%d)", got, HealthStopped)
	}
}

func TestAggregateHealthFromTopo_DisabledExcluded(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"nginx":      ui.NodeRunning,
		"app-main":   ui.NodeRunning,
		"app-second": ui.NodeDisabled,
	}
	if got := AggregateHealthFromTopo(topo); got != HealthRunning {
		t.Errorf("AggregateHealthFromTopo = %d, want HealthRunning (%d) (disabled should not count)", got, HealthRunning)
	}
}

func TestAggregateHealthFromTopo_OnlyDisabled(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"app-second": ui.NodeDisabled,
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

// --- HasRuntimeStatuses ---

func TestHasRuntimeStatuses_EmptyMap(t *testing.T) {
	if HasRuntimeStatuses(nil) {
		t.Error("expected false for nil map")
	}
	if HasRuntimeStatuses(map[string]ui.NodeStatus{}) {
		t.Error("expected false for empty map")
	}
}

func TestHasRuntimeStatuses_OnlyDisabled(t *testing.T) {
	m := map[string]ui.NodeStatus{"web": ui.NodeDisabled}
	if HasRuntimeStatuses(m) {
		t.Error("expected false when only disabled nodes")
	}
}

func TestHasRuntimeStatuses_WithRunning(t *testing.T) {
	m := map[string]ui.NodeStatus{"web": ui.NodeRunning}
	if !HasRuntimeStatuses(m) {
		t.Error("expected true when running node exists")
	}
}

func TestHasRuntimeStatuses_WithStopped(t *testing.T) {
	m := map[string]ui.NodeStatus{"web": ui.NodeStopped}
	if !HasRuntimeStatuses(m) {
		t.Error("expected true when stopped (non-disabled) node exists")
	}
}
