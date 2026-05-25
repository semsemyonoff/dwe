package setup

import (
	"testing"

	"devbox-cli/internal/validate/env"
)

// Test coerceInputAnswers with various input question types.
func TestCoerceInputAnswers(t *testing.T) {
	tests := []struct {
		name      string
		questions []Question
		raws      map[string]string
		want      map[string]any
		wantErr   bool
	}{
		{
			name: "port preset happy path",
			questions: []Question{
				{
					ID:   "port_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Preset: PresetPort,
					},
				},
			},
			raws: map[string]string{
				"port_input": "8080",
			},
			want: map[string]any{
				"port_input": 8080,
			},
		},
		{
			name: "port preset out of range",
			questions: []Question{
				{
					ID:   "port_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Preset: PresetPort,
					},
				},
			},
			raws: map[string]string{
				"port_input": "99999",
			},
			wantErr: true,
		},
		{
			name: "port preset non-numeric",
			questions: []Question{
				{
					ID:   "port_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Preset: PresetPort,
					},
				},
			},
			raws: map[string]string{
				"port_input": "abc",
			},
			wantErr: true,
		},
		{
			name: "non-preset input returns string unchanged",
			questions: []Question{
				{
					ID:       "text_input",
					Type:     TypeInput,
					Validate: nil,
				},
			},
			raws: map[string]string{
				"text_input": "hello world",
			},
			want: map[string]any{
				"text_input": "hello world",
			},
		},
		{
			name: "hostname preset happy path",
			questions: []Question{
				{
					ID:   "hostname_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Preset: PresetHostname,
					},
				},
			},
			raws: map[string]string{
				"hostname_input": "api.example.com",
			},
			want: map[string]any{
				"hostname_input": "api.example.com",
			},
		},
		{
			name: "hostname preset invalid",
			questions: []Question{
				{
					ID:   "hostname_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Preset: PresetHostname,
					},
				},
			},
			raws: map[string]string{
				"hostname_input": "bad host!",
			},
			wantErr: true,
		},
		{
			name: "path preset happy path",
			questions: []Question{
				{
					ID:   "path_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Preset: PresetPath,
					},
				},
			},
			raws: map[string]string{
				"path_input": "/usr/local/bin",
			},
			want: map[string]any{
				"path_input": "/usr/local/bin",
			},
		},
		{
			name: "non-empty preset happy path",
			questions: []Question{
				{
					ID:   "nonempty_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Preset: PresetNonEmpty,
					},
				},
			},
			raws: map[string]string{
				"nonempty_input": "   hello   ",
			},
			want: map[string]any{
				"nonempty_input": "hello",
			},
		},
		{
			name: "regex preset happy path",
			questions: []Question{
				{
					ID:   "regex_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Regex: "^[a-z]+$",
					},
				},
			},
			raws: map[string]string{
				"regex_input": "abc",
			},
			want: map[string]any{
				"regex_input": "abc",
			},
		},
		{
			name: "regex preset non-match",
			questions: []Question{
				{
					ID:   "regex_input",
					Type: TypeInput,
					Validate: &ValidateSpec{
						Regex: "^[a-z]+$",
					},
				},
			},
			raws: map[string]string{
				"regex_input": "ABC123",
			},
			wantErr: true,
		},
		{
			name:      "empty input questions returns empty map",
			questions: []Question{},
			raws:      map[string]string{},
			want:      map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coerceInputAnswers(tt.questions, tt.raws)
			if (err != nil) != tt.wantErr {
				t.Errorf("coerceInputAnswers() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return // Expected error, nothing more to check.
			}
			if len(got) != len(tt.want) {
				t.Errorf("coerceInputAnswers() len = %d, want %d", len(got), len(tt.want))
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("coerceInputAnswers()[%q] = %v (%T), want %v (%T)", k, got[k], got[k], v, v)
				}
			}
		})
	}
}

// Test coercePortOverrides with various port values.
func TestCoercePortOverrides(t *testing.T) {
	tests := []struct {
		name      string
		conflicts []env.PortConflict
		raws      map[string]string
		want      map[PortKey]int
		wantErr   bool
	}{
		{
			name: "single port happy path",
			conflicts: []env.PortConflict{
				{
					Service:       "web",
					PortName:      "http",
					RequestedPort: 8080,
					OccupiedBy:    "nginx",
				},
			},
			raws: map[string]string{
				"web/http": "9000",
			},
			want: map[PortKey]int{
				{Service: "web", PortName: "http"}: 9000,
			},
		},
		{
			name: "multiple ports happy path",
			conflicts: []env.PortConflict{
				{
					Service:       "web",
					PortName:      "http",
					RequestedPort: 8080,
					OccupiedBy:    "nginx",
				},
				{
					Service:       "api",
					PortName:      "grpc",
					RequestedPort: 50051,
					OccupiedBy:    "other service",
				},
			},
			raws: map[string]string{
				"web/http": "9000",
				"api/grpc": "60000",
			},
			want: map[PortKey]int{
				{Service: "web", PortName: "http"}: 9000,
				{Service: "api", PortName: "grpc"}: 60000,
			},
		},
		{
			name: "port out of range high",
			conflicts: []env.PortConflict{
				{
					Service:       "web",
					PortName:      "http",
					RequestedPort: 8080,
					OccupiedBy:    "nginx",
				},
			},
			raws: map[string]string{
				"web/http": "99999",
			},
			wantErr: true,
		},
		{
			name: "port out of range low",
			conflicts: []env.PortConflict{
				{
					Service:       "web",
					PortName:      "http",
					RequestedPort: 8080,
					OccupiedBy:    "nginx",
				},
			},
			raws: map[string]string{
				"web/http": "0",
			},
			wantErr: true,
		},
		{
			name: "port negative",
			conflicts: []env.PortConflict{
				{
					Service:       "web",
					PortName:      "http",
					RequestedPort: 8080,
					OccupiedBy:    "nginx",
				},
			},
			raws: map[string]string{
				"web/http": "-1",
			},
			wantErr: true,
		},
		{
			name: "port non-numeric",
			conflicts: []env.PortConflict{
				{
					Service:       "web",
					PortName:      "http",
					RequestedPort: 8080,
					OccupiedBy:    "nginx",
				},
			},
			raws: map[string]string{
				"web/http": "abc",
			},
			wantErr: true,
		},
		{
			name:      "empty port conflicts returns empty map",
			conflicts: []env.PortConflict{},
			raws:      map[string]string{},
			want:      map[PortKey]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coercePortOverrides(tt.conflicts, tt.raws)
			if (err != nil) != tt.wantErr {
				t.Errorf("coercePortOverrides() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return // Expected error, nothing more to check.
			}
			if len(got) != len(tt.want) {
				t.Errorf("coercePortOverrides() len = %d, want %d", len(got), len(tt.want))
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("coercePortOverrides()[%v] = %d, want %d", k, got[k], v)
				}
			}
		})
	}
}
