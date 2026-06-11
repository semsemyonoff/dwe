package config

import "testing"

func TestVarsWritableAllows(t *testing.T) {
	patterns := []string{"vars.db.*", "vars.app.name", "vars.feature.flags.*"}
	tests := []struct {
		target string
		want   bool
	}{
		{"vars.db.host", true},         // wildcard descendant
		{"vars.db.creds.user", true},   // wildcard deeper descendant
		{"vars.db", false},             // wildcard base itself is denied
		{"vars.dbx.host", false},       // dot-boundary: dbx is not db
		{"vars.database.host", false},  // dot-boundary: database is not db
		{"vars.app.name", true},        // exact
		{"vars.app.namex", false},      // exact requires identical path
		{"vars.app", false},            // exact parent not allowed
		{"vars.feature.flags.x", true}, // nested wildcard
		{"vars.feature.flags", false},  // nested wildcard base denied
		{"vars.other.thing", false},    // unlisted
	}
	for _, tc := range tests {
		if got := VarsWritableAllows(patterns, tc.target); got != tc.want {
			t.Errorf("VarsWritableAllows(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

func TestVarsWritableAllows_MalformedFailsClosed(t *testing.T) {
	tests := []struct {
		patterns []string
		target   string
	}{
		{[]string{""}, "vars.db.host"},               // empty pattern
		{[]string{".*"}, "vars.db.host"},             // empty base wildcard
		{[]string{"*"}, "vars.db.host"},              // bare star
		{[]string{"vars.*.host"}, "vars.db.host"},    // interior wildcard unsupported
		{[]string{"vars.d*b.host"}, "vars.dab.host"}, // star in exact pattern
	}
	for _, tc := range tests {
		if VarsWritableAllows(tc.patterns, tc.target) {
			t.Errorf("VarsWritableAllows(%v, %q) = true, want false (fail closed)", tc.patterns, tc.target)
		}
	}
}

func TestBridgeVarsWritable_NilSafe(t *testing.T) {
	if got := BridgeVarsWritable(nil); got != nil {
		t.Errorf("BridgeVarsWritable(nil) = %v, want nil", got)
	}
	if got := BridgeVarsWritable(&DweConfig{}); got != nil {
		t.Errorf("BridgeVarsWritable(no bridge) = %v, want nil", got)
	}
	cfg := &DweConfig{Bridge: &BridgeConfig{VarsWritable: []string{"vars.db.*"}}}
	got := BridgeVarsWritable(cfg)
	if len(got) != 1 || got[0] != "vars.db.*" {
		t.Errorf("BridgeVarsWritable = %v, want [vars.db.*]", got)
	}
}

func TestLoadConfig_bridge_3LayerMerge(t *testing.T) {
	// workspace.yml sets a base allowlist; local.yml replaces it (list is
	// last-layer-wins via deepMerge), so a developer may widen/narrow it.
	ws := sampleWorkspaceYML + "\nbridge:\n  vars_writable:\n    - vars.db.host\n"
	lc := "bridge:\n  vars_writable:\n    - vars.db.host\n    - vars.app.name\n"
	path := writeFullFixture(t, ws, "", lc, "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := BridgeVarsWritable(cfg)
	if len(got) != 2 || got[0] != "vars.db.host" || got[1] != "vars.app.name" {
		t.Errorf("merged vars_writable = %v, want [vars.db.host vars.app.name]", got)
	}
}

func TestLoadConfig_bridge_absent(t *testing.T) {
	path := writeFullFixture(t, sampleWorkspaceYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := BridgeVarsWritable(cfg); got != nil {
		t.Errorf("absent bridge: want nil, got %v", got)
	}
}
