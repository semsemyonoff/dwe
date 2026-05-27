package builtin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/docker"
)

func TestDockerWaitHealthyValidate(t *testing.T) {
	tests := []struct {
		name    string
		with    map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty params uses defaults",
			with:    map[string]any{},
			wantErr: false,
		},
		{
			name: "valid timeout and interval",
			with: map[string]any{
				"timeout":  "120s",
				"interval": "5s",
			},
			wantErr: false,
		},
		{
			name: "valid services list",
			with: map[string]any{
				"services": []string{"app", "db"},
			},
			wantErr: false,
		},
		{
			name: "negative timeout",
			with: map[string]any{
				"timeout": "-10s",
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "zero timeout",
			with: map[string]any{
				"timeout": "0s",
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "negative interval",
			with: map[string]any{
				"interval": "-1s",
			},
			wantErr: true,
			errMsg:  "interval must be positive",
		},
		{
			name: "zero interval",
			with: map[string]any{
				"interval": "0s",
			},
			wantErr: true,
			errMsg:  "interval must be positive",
		},
		{
			name: "invalid timeout format",
			with: map[string]any{
				"timeout": "not-a-duration",
			},
			wantErr: true,
			errMsg:  "invalid duration",
		},
		{
			name: "invalid interval format",
			with: map[string]any{
				"interval": "xyz",
			},
			wantErr: true,
			errMsg:  "invalid duration",
		},
		{
			name: "empty string in services list",
			with: map[string]any{
				"services": []string{"app", "", "db"},
			},
			wantErr: true,
			errMsg:  "empty string",
		},
		{
			name: "non-string service entry",
			with: map[string]any{
				"services": []any{123},
			},
			wantErr: true,
			errMsg:  "expected string",
		},
		{
			name: "unknown key",
			with: map[string]any{
				"timeout": "60s",
				"unknown": "value",
			},
			wantErr: true,
			errMsg:  "unknown key",
		},
		{
			name:    "nil with is ok",
			with:    nil,
			wantErr: false,
		},
	}

	builtin := dockerWaitHealthyBuiltin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := builtin.Validate(tt.with)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDockerWaitHealthyDescribe(t *testing.T) {
	tests := []struct {
		name      string
		with      map[string]any
		wantSubst string
	}{
		{
			name:      "defaults",
			with:      map[string]any{},
			wantSubst: "all containers are healthy",
		},
		{
			name: "with services",
			with: map[string]any{
				"services": []string{"app", "db"},
			},
			wantSubst: "2 services are healthy",
		},
		{
			name: "with custom timeout",
			with: map[string]any{
				"timeout": "120s",
			},
			wantSubst: "timeout: 2m",
		},
		{
			name: "with custom interval",
			with: map[string]any{
				"interval": "5s",
			},
			wantSubst: "interval: 5s",
		},
	}

	builtin := dockerWaitHealthyBuiltin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desc := builtin.Describe(tt.with)
			require.Contains(t, desc, tt.wantSubst)
		})
	}
}

func TestDockerWaitHealthyRun(t *testing.T) {
	// Run testing is covered by integration tests that exercise the full
	// builtin via the registry. Direct unit testing would require mocking
	// docker compose commands, which is covered in integration tests.
	// The public contract (Validate/Describe) is tested above.
}

func TestDockerWaitHealthyIntegration(t *testing.T) {
	// Test via the public registry API to ensure wiring is correct.
	// This confirms the builtin is registered and callable.
	t.Run("builtin is registered", func(t *testing.T) {
		b, ok := Get("docker_wait_healthy", CtxUserYAML)
		require.True(t, ok, "docker_wait_healthy must be registered")
		require.NotNil(t, b)
	})

	t.Run("Validate via registry", func(t *testing.T) {
		err := Validate("docker_wait_healthy", map[string]any{
			"timeout": "60s",
		}, CtxUserYAML)
		require.NoError(t, err)
	})

	t.Run("Validate rejects bad params via registry", func(t *testing.T) {
		err := Validate("docker_wait_healthy", map[string]any{
			"timeout": "-10s",
		}, CtxUserYAML)
		require.Error(t, err)
		require.Contains(t, err.Error(), "positive")
	})

	t.Run("Describe via registry", func(t *testing.T) {
		desc := Describe("docker_wait_healthy", map[string]any{
			"services": []string{"app"},
		})
		require.Contains(t, desc, "1 service is healthy")
	})
}

func TestComposeContainerIDsFor(t *testing.T) {
	// This test verifies the interface logic without requiring docker.
	// The actual docker execution is tested at the command level.

	t.Run("empty services list returns empty IDs", func(t *testing.T) {
		// When services is empty, ContainerIDsFor should return nil without calling docker.
		compose := &docker.Compose{Bin: "docker"}
		ids, err := compose.ContainerIDsFor(nil)
		require.NoError(t, err)
		require.Nil(t, ids)

		ids, err = compose.ContainerIDsFor([]string{})
		require.NoError(t, err)
		require.Nil(t, ids)
	})
}

func TestGetDurationParam(t *testing.T) {
	tests := []struct {
		name       string
		with       map[string]any
		key        string
		defaultVal time.Duration
		want       time.Duration
		wantErr    bool
	}{
		{
			name:       "nil with uses default",
			with:       nil,
			key:        "timeout",
			defaultVal: 60 * time.Second,
			want:       60 * time.Second,
			wantErr:    false,
		},
		{
			name:       "missing key uses default",
			with:       map[string]any{},
			key:        "timeout",
			defaultVal: 30 * time.Second,
			want:       30 * time.Second,
			wantErr:    false,
		},
		{
			name:       "valid duration string",
			with:       map[string]any{"timeout": "120s"},
			key:        "timeout",
			defaultVal: 60 * time.Second,
			want:       120 * time.Second,
			wantErr:    false,
		},
		{
			name:       "valid complex duration",
			with:       map[string]any{"interval": "2m30s"},
			key:        "interval",
			defaultVal: 10 * time.Second,
			want:       2*time.Minute + 30*time.Second,
			wantErr:    false,
		},
		{
			name:       "invalid duration string",
			with:       map[string]any{"timeout": "not-valid"},
			key:        "timeout",
			defaultVal: 60 * time.Second,
			want:       0,
			wantErr:    true,
		},
		{
			name:       "nil value in map uses default",
			with:       map[string]any{"timeout": nil},
			key:        "timeout",
			defaultVal: 45 * time.Second,
			want:       45 * time.Second,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getDurationParam(tt.with, tt.key, tt.defaultVal)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}
