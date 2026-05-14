package journal

import (
	"testing"

	"devbox-cli/internal/config"

	"github.com/stretchr/testify/assert"
)

// TestActionHash verifies that ActionHash produces consistent hashes
// and that key order in the with map doesn't affect the result.
func TestActionHash(t *testing.T) {
	tests := []struct {
		name    string
		action  config.Action
		want    string // Just verify it's non-empty and stable
		wantErr bool
	}{
		{
			name: "shell action with no with",
			action: config.Action{
				Type: "shell",
				Cmd:  "echo hello",
				With: nil,
			},
		},
		{
			name: "devbox action",
			action: config.Action{
				Type: "devbox",
				Cmd:  "run something",
				With: nil,
			},
		},
		{
			name: "command action with with",
			action: config.Action{
				Type: "command",
				Cmd:  "my-cmd",
				With: map[string]any{
					"arg1": "value1",
					"arg2": "value2",
				},
			},
		},
		{
			name: "builtin action with with",
			action: config.Action{
				Type: "builtin",
				Cmd:  "service_dirs_ensure",
				With: map[string]any{
					"mode": "recreate",
				},
			},
		},
		{
			name: "empty action",
			action: config.Action{
				Type: "",
				Cmd:  "",
				With: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1 := ActionHash(tt.action)
			// Verify hash is a valid hex string of expected length
			assert.Len(t, hash1, 64, "ActionHash should return full sha256 (64 hex chars)")

			// Verify hash is stable
			hash2 := ActionHash(tt.action)
			assert.Equal(t, hash1, hash2, "ActionHash should be stable")
		})
	}
}

// TestActionHashMapKeyOrder verifies that key order in the with map
// doesn't affect the resulting hash (order-independence).
func TestActionHashMapKeyOrder(t *testing.T) {
	withMap1 := map[string]any{
		"arg1": "value1",
		"arg2": "value2",
		"arg3": "value3",
	}

	withMap2 := map[string]any{
		"arg3": "value3",
		"arg1": "value1",
		"arg2": "value2",
	}

	action1 := config.Action{
		Type: "builtin",
		Cmd:  "message",
		With: withMap1,
	}

	action2 := config.Action{
		Type: "builtin",
		Cmd:  "message",
		With: withMap2,
	}

	hash1 := ActionHash(action1)
	hash2 := ActionHash(action2)

	assert.Equal(t, hash1, hash2, "ActionHash should be invariant to key order in with map")
}

// TestShortHash verifies the short hash utility function.
func TestShortHash(t *testing.T) {
	fullHash := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	shortHash := ShortHash(fullHash)
	assert.Equal(t, "abcdef012345", shortHash)

	// Short input
	shortInput := "abcd"
	assert.Equal(t, "abcd", ShortHash(shortInput))
}

// TestServiceConfigHash verifies that ServiceConfigHash produces consistent,
// order-independent hashes.
func TestServiceConfigHash(t *testing.T) {
	svc := config.ServiceConfig{
		Type:            "app",
		Container:       "main",
		Mandatory:       false,
		Dir:             "/app",
		DirInternal:     "/src",
		WorkDirInternal: "/src",
		Extends:         "",
		DependsOn:       []string{"db"},
		Compose:         []string{"docker-compose.yml"},
	}

	// Test with nil deploy config
	hash1 := ServiceConfigHash(svc, nil)
	assert.Len(t, hash1, 64)

	// Test with deploy config
	deployCfg := &config.DeployConfig{
		Phases: []config.DeployPhase{
			{
				Name:           "setup",
				Steps:          []config.DeployStep{},
				DeployServices: false,
			},
		},
	}
	hash2 := ServiceConfigHash(svc, deployCfg)
	assert.Len(t, hash2, 64)
	assert.NotEqual(t, hash1, hash2, "Adding deploy config should change hash")

	// Test stability
	hash3 := ServiceConfigHash(svc, deployCfg)
	assert.Equal(t, hash2, hash3)
}

// TestProjectConfigHash verifies the project config hash with tracked services.
func TestProjectConfigHash(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:      "app",
				Container: "main",
			},
			"debug": {
				Type:      "app",
				Container: "debug",
				Extends:   "main",
			},
		},
	}

	deployCfg := &config.DeployConfig{}
	svcDeploys := map[string]*config.DeployConfig{
		"main": nil,
	}

	// Only "main" is tracked
	trackedServices := []string{"main"}

	hash1 := ProjectConfigHash(cfg, deployCfg, svcDeploys, trackedServices)
	assert.Len(t, hash1, 64)

	// Stability check
	hash2 := ProjectConfigHash(cfg, deployCfg, svcDeploys, trackedServices)
	assert.Equal(t, hash1, hash2)

	// Hash should change if we add "debug" to tracked services
	hash3 := ProjectConfigHash(cfg, deployCfg, svcDeploys, []string{"main", "debug"})
	assert.NotEqual(t, hash1, hash3)
}

// TestProjectConfigHashIgnoresUntracked verifies that changes to untracked
// services do not affect the project config hash.
func TestProjectConfigHashIgnoresUntracked(t *testing.T) {
	cfg1 := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:      "app",
				Container: "main",
			},
			"debug": {
				Type:      "app",
				Container: "debug",
				Extends:   "main",
			},
		},
	}

	cfg2 := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:      "app",
				Container: "main",
			},
			"debug": {
				Type:      "app",
				Container: "debug-modified", // Changed, but not tracked
				Extends:   "main",
			},
		},
	}

	deployCfg := &config.DeployConfig{}
	svcDeploys := map[string]*config.DeployConfig{"main": nil}
	trackedServices := []string{"main"} // Only main is tracked

	hash1 := ProjectConfigHash(cfg1, deployCfg, svcDeploys, trackedServices)
	hash2 := ProjectConfigHash(cfg2, deployCfg, svcDeploys, trackedServices)

	assert.Equal(t, hash1, hash2, "Untracked service changes should not affect project hash")
}

// TestCanonicalMap verifies that the canonical map marshalling is order-independent.
func TestCanonicalMap(t *testing.T) {
	m1 := map[string]any{
		"z": "value",
		"a": "value",
		"m": "value",
	}

	m2 := map[string]any{
		"a": "value",
		"m": "value",
		"z": "value",
	}

	bytes1 := canonicalMap(m1)
	bytes2 := canonicalMap(m2)

	assert.Equal(t, bytes1, bytes2, "canonicalMap should produce order-independent output")
}

// TestCanonicalMapNestedStructure tests canonical marshalling with nested maps.
func TestCanonicalMapNestedStructure(t *testing.T) {
	m := map[string]any{
		"nested": map[string]any{
			"z": "value",
			"a": "value",
		},
		"array": []any{
			map[string]any{"b": 2, "a": 1},
		},
	}

	bytes1 := canonicalMap(m)
	bytes2 := canonicalMap(m)

	assert.Equal(t, bytes1, bytes2, "canonicalMap should be stable for nested structures")
}

// TestHashesNotEmptyOnEmptyInput verifies that hashes are produced
// even for empty or zero-value inputs.
func TestHashesNotEmptyOnEmptyInput(t *testing.T) {
	emptyAction := config.Action{}
	hash := ActionHash(emptyAction)
	assert.Len(t, hash, 64)
	assert.NotEmpty(t, hash)

	emptySvc := config.ServiceConfig{}
	svcHash := ServiceConfigHash(emptySvc, nil)
	assert.Len(t, svcHash, 64)
	assert.NotEmpty(t, svcHash)

	cfg := &config.DevboxConfig{Services: map[string]config.ServiceConfig{}}
	projHash := ProjectConfigHash(cfg, nil, map[string]*config.DeployConfig{}, []string{})
	assert.Len(t, projHash, 64)
	assert.NotEmpty(t, projHash)
}
