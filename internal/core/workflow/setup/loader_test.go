package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadSetupYAML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    *Config
		wantErr bool
	}{
		{
			name:    "missing file",
			content: "", // Special marker for missing file
			want:    nil,
			wantErr: false,
		},
		{
			name:    "empty file",
			content: "",
			want:    &Config{Questions: nil},
			wantErr: false,
		},
		{
			name: "single input question",
			content: `questions:
  - id: app_name
    type: input
    title: Application Name
    description: What is your app called?
    required: true
    writes: app.name`,
			want: &Config{
				Questions: []Question{
					{
						ID:          "app_name",
						Type:        "input",
						Title:       "Application Name",
						Description: "What is your app called?",
						Required:    true,
						Writes:      "app.name",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "select question",
			content: `questions:
  - id: env
    type: select
    title: Environment
    writes: deploy.environment
    options:
      - value: dev
        label: Development
      - value: prod
        label: Production`,
			want: &Config{
				Questions: []Question{
					{
						ID:     "env",
						Type:   "select",
						Title:  "Environment",
						Writes: "deploy.environment",
						Options: []Option{
							{Value: "dev", Label: "Development"},
							{Value: "prod", Label: "Production"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "multiselect question",
			content: `questions:
  - id: features
    type: multiselect
    title: Select Features
    writes: app.features
    options:
      - value: auth
        label: Authentication
      - value: api
        label: REST API`,
			want: &Config{
				Questions: []Question{
					{
						ID:     "features",
						Type:   "multiselect",
						Title:  "Select Features",
						Writes: "app.features",
						Options: []Option{
							{Value: "auth", Label: "Authentication"},
							{Value: "api", Label: "REST API"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "confirm question",
			content: `questions:
  - id: use_cache
    type: confirm
    title: Enable Caching?
    writes: app.use_cache`,
			want: &Config{
				Questions: []Question{
					{
						ID:     "use_cache",
						Type:   "confirm",
						Title:  "Enable Caching?",
						Writes: "app.use_cache",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "input with validate preset",
			content: `questions:
  - id: port
    type: input
    title: Port Number
    writes: app.port
    validate:
      preset: port`,
			want: &Config{
				Questions: []Question{
					{
						ID:     "port",
						Type:   "input",
						Title:  "Port Number",
						Writes: "app.port",
						Validate: &ValidateSpec{
							Preset: "port",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "input with validate regex",
			content: `questions:
  - id: hostname
    type: input
    title: Hostname
    writes: app.hostname
    validate:
      regex: '^[a-z][a-z0-9-]*[a-z0-9]$'`,
			want: &Config{
				Questions: []Question{
					{
						ID:     "hostname",
						Type:   "input",
						Title:  "Hostname",
						Writes: "app.hostname",
						Validate: &ValidateSpec{
							Regex: "^[a-z][a-z0-9-]*[a-z0-9]$",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "multiple questions",
			content: `questions:
  - id: name
    type: input
    title: Name
    writes: app.name
  - id: port
    type: input
    title: Port
    writes: app.port
    validate:
      preset: port`,
			want: &Config{
				Questions: []Question{
					{
						ID:     "name",
						Type:   "input",
						Title:  "Name",
						Writes: "app.name",
					},
					{
						ID:     "port",
						Type:   "input",
						Title:  "Port",
						Writes: "app.port",
						Validate: &ValidateSpec{
							Preset: "port",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "unknown top-level field",
			content: `questions: []
unknown_field: value`,
			want:    nil,
			wantErr: true,
		},
		{
			name: "unknown field inside question",
			content: `questions:
  - id: test
    type: input
    title: Test
    writes: test
    unknown_field: value`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "null questions field",
			content: `questions:`,
			want:    &Config{Questions: nil},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "setup.yml")

			if tt.name == "missing file" {
				// Don't create the file for the missing file case
				got, err := LoadSetupYAML(path)
				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, tt.want, got)
			} else {
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))

				got, err := LoadSetupYAML(path)
				if tt.wantErr {
					require.Error(t, err)
					require.Nil(t, got)
				} else {
					require.NoError(t, err)
					require.Equal(t, tt.want, got)
				}
			}
		})
	}
}

func TestLoadSetupYAMLEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "setup.yml")

	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	got, err := LoadSetupYAML(path)
	require.NoError(t, err)
	require.Equal(t, &Config{Questions: nil}, got)
}

func TestLoadSetupYAMLValidContent(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "setup.yml")

	content := `questions:
  - id: name
    type: input
    title: Project Name
    required: true
    writes: project.name
  - id: port
    type: input
    title: Port
    writes: services.web.ports.http
    validate:
      preset: port`

	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	got, err := LoadSetupYAML(path)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Questions, 2)
	require.Equal(t, "name", got.Questions[0].ID)
	require.Equal(t, "port", got.Questions[1].ID)
	require.NotNil(t, got.Questions[1].Validate)
	require.Equal(t, "port", got.Questions[1].Validate.Preset)
}
