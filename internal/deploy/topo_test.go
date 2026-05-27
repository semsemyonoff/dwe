package deploy_test

import (
	"errors"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
)

func makeDeployMap(t *testing.T, entries map[string][]string) map[string]*config.DeployConfig {
	t.Helper()
	result := make(map[string]*config.DeployConfig, len(entries))
	for name, after := range entries {
		dc := &config.DeployConfig{After: after}
		result[name] = dc
	}
	return result
}

func makeServicesMap(names ...string) map[string]config.ServiceConfig {
	m := make(map[string]config.ServiceConfig, len(names))
	for _, n := range names {
		m[n] = config.ServiceConfig{}
	}
	return m
}

func TestTopoSortByAfter_linearChain(t *testing.T) {
	// c after [b], b after [a] → [a, b, c]
	deploys := makeDeployMap(t, map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"b"},
	})
	services := makeServicesMap("a", "b", "c")
	order, err := deploy.TopoSortByAfter(deploys, services)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestTopoSortByAfter_diamond(t *testing.T) {
	// c after [a, b], d after [c] → [a, b, c, d] with a < b alphabetical tie-break
	deploys := makeDeployMap(t, map[string][]string{
		"a": nil,
		"b": nil,
		"c": {"a", "b"},
		"d": {"c"},
	})
	services := makeServicesMap("a", "b", "c", "d")
	order, err := deploy.TopoSortByAfter(deploys, services)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c", "d"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestTopoSortByAfter_noAfterAlphabetical(t *testing.T) {
	// No after: declared → alphabetical service list
	deploys := makeDeployMap(t, map[string][]string{
		"zebra": nil,
		"apple": nil,
		"mango": nil,
	})
	services := makeServicesMap("zebra", "apple", "mango")
	order, err := deploy.TopoSortByAfter(deploys, services)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"apple", "mango", "zebra"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestTopoSortByAfter_cycle(t *testing.T) {
	// a after [b], b after [a] → ErrDeployCycle
	deploys := makeDeployMap(t, map[string][]string{
		"a": {"b"},
		"b": {"a"},
	})
	services := makeServicesMap("a", "b")
	_, err := deploy.TopoSortByAfter(deploys, services)
	if err == nil {
		t.Fatal("expected ErrDeployCycle, got nil")
	}
	if !errors.Is(err, deploy.ErrDeployCycle) {
		t.Errorf("err = %v, want wraps ErrDeployCycle", err)
	}
}

func TestTopoSortByAfter_cyclePath(t *testing.T) {
	// three-node cycle: a→b→c→a
	deploys := makeDeployMap(t, map[string][]string{
		"a": {"c"},
		"b": {"a"},
		"c": {"b"},
	})
	services := makeServicesMap("a", "b", "c")
	_, err := deploy.TopoSortByAfter(deploys, services)
	if err == nil {
		t.Fatal("expected ErrDeployCycle, got nil")
	}
	if !errors.Is(err, deploy.ErrDeployCycle) {
		t.Errorf("err = %v, want wraps ErrDeployCycle", err)
	}
}

func TestTopoSortByAfter_selfReference(t *testing.T) {
	deploys := makeDeployMap(t, map[string][]string{
		"a": {"a"},
	})
	services := makeServicesMap("a")
	_, err := deploy.TopoSortByAfter(deploys, services)
	if err == nil {
		t.Fatal("expected ErrDeploySelfReference, got nil")
	}
	if !errors.Is(err, deploy.ErrDeploySelfReference) {
		t.Errorf("err = %v, want wraps ErrDeploySelfReference", err)
	}
}

func makeMandatoryServicesMap(spec map[string]bool) map[string]config.ServiceConfig {
	m := make(map[string]config.ServiceConfig, len(spec))
	for name, mandatory := range spec {
		m[name] = config.ServiceConfig{Required: mandatory}
	}
	return m
}

func TestTopoSortByAfter_mandatoryBeforeOptional(t *testing.T) {
	// "b" is mandatory; "a" and "c" are optional. Alphabetical tie-break would
	// produce [a, b, c]; the mandatory partition must bubble b to the front
	// regardless of after-graph (none declared here).
	deploys := makeDeployMap(t, map[string][]string{
		"a": nil,
		"b": nil,
		"c": nil,
	})
	services := makeMandatoryServicesMap(map[string]bool{
		"a": false,
		"b": true,
		"c": false,
	})
	order, err := deploy.TopoSortByAfter(deploys, services)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"b", "a", "c"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestTopoSortByAfter_mandatoryAfterOptionalRejected(t *testing.T) {
	// "m" is mandatory and tries to deploy after optional "o" → error.
	deploys := makeDeployMap(t, map[string][]string{
		"m": {"o"},
		"o": nil,
	})
	services := makeMandatoryServicesMap(map[string]bool{
		"m": true,
		"o": false,
	})
	_, err := deploy.TopoSortByAfter(deploys, services)
	if err == nil {
		t.Fatal("expected ErrMandatoryAfterOptional, got nil")
	}
	if !errors.Is(err, deploy.ErrMandatoryAfterOptional) {
		t.Errorf("err = %v, want wraps ErrMandatoryAfterOptional", err)
	}
}

func TestTopoSortByAfter_mandatoryAfterMandatoryAllowed(t *testing.T) {
	// Mandatory after another mandatory is fine (within-bucket ordering).
	deploys := makeDeployMap(t, map[string][]string{
		"m1": nil,
		"m2": {"m1"},
		"o":  nil,
	})
	services := makeMandatoryServicesMap(map[string]bool{
		"m1": true,
		"m2": true,
		"o":  false,
	})
	order, err := deploy.TopoSortByAfter(deploys, services)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"m1", "m2", "o"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestTopoSortByAfter_unknownAfterRef(t *testing.T) {
	deploys := makeDeployMap(t, map[string][]string{
		"a": {"nonexistent"},
	})
	services := makeServicesMap("a") // "nonexistent" not in services
	_, err := deploy.TopoSortByAfter(deploys, services)
	if err == nil {
		t.Fatal("expected ErrDeployUnknownAfterRef, got nil")
	}
	if !errors.Is(err, deploy.ErrDeployUnknownAfterRef) {
		t.Errorf("err = %v, want wraps ErrDeployUnknownAfterRef", err)
	}
}

func TestTopoSortByAfter_missingDeployAncestorSilentlyDropped(t *testing.T) {
	// "a" after ["b"], "b" in services but no deploy.yml → edge dropped, [a] returned
	deploys := makeDeployMap(t, map[string][]string{
		"a": {"b"},
	})
	services := makeServicesMap("a", "b") // b exists in services but not in deploys
	order, err := deploy.TopoSortByAfter(deploys, services)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 1 || order[0] != "a" {
		t.Errorf("order = %v, want [a]", order)
	}
}
