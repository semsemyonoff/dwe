package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/validate"
)

func TestDevboxValidator(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantDiags     int
		wantSchema    validate.Severity
		wantDevbox    validate.Severity
		wantSchemaMsg string
	}{
		{
			name:          "missing schema",
			fixture:       "devbox-missing-schema",
			wantDiags:     2,
			wantSchema:    validate.SeverityError,
			wantDevbox:    validate.SeverityOK, // LoadConfig succeeds without schema validation
			wantSchemaMsg: "schema_version",
		},
		{
			name:          "legacy schema",
			fixture:       "devbox-legacy-schema",
			wantDiags:     2,
			wantSchema:    validate.SeverityError,
			wantDevbox:    validate.SeverityOK, // LoadConfig succeeds without schema validation
			wantSchemaMsg: "schema_version",
		},
		{
			name:       "bad keys",
			fixture:    "devbox-v2-bad-keys",
			wantDiags:  2,
			wantSchema: validate.SeverityOK,
			wantDevbox: validate.SeverityError,
		},
		{
			name:       "good config",
			fixture:    "devbox-v2-good",
			wantDiags:  2,
			wantSchema: validate.SeverityOK,
			wantDevbox: validate.SeverityOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixturePath := filepath.Join("testdata", tt.fixture)
			ctx := validate.Context{
				ProjectRoot: fixturePath,
				ConfigPath:  filepath.Join(fixturePath, "devbox.yml"),
			}

			v := &devboxValidator{}
			diags := v.Run(ctx)

			t.Logf("fixture=%s: got %d diags", tt.fixture, len(diags))
			for i, d := range diags {
				t.Logf("  [%d] severity=%v target=%s file=%s message=%s", i, d.Severity, d.Target, d.File, d.Message)
			}

			require.Equal(t, tt.wantDiags, len(diags), "diagnostic count mismatch")

			if len(diags) > 0 {
				// First diagnostic should be schema check
				require.Equal(t, tt.wantSchema, diags[0].Severity)
				require.Equal(t, "config.devbox.schema", diags[0].Target)
			}

			if len(diags) > 1 {
				// Second diagnostic should be devbox load
				require.Equal(t, tt.wantDevbox, diags[1].Severity)
				require.Equal(t, "config.devbox", diags[1].Target)
			}

			if tt.wantSchemaMsg != "" && len(diags) > 0 {
				require.Contains(t, diags[0].Message, tt.wantSchemaMsg)
			}
		})
	}
}

func TestDevboxValidatorID(t *testing.T) {
	v := &devboxValidator{}
	require.Equal(t, "devbox", v.ID())
	require.Equal(t, "config", v.Domain())
}
