package setup

import (
	"testing"
)

func TestBuildOverlay(t *testing.T) {
	tests := []struct {
		name      string
		questions []Question
		answers   map[string]any
		want      map[string]any
		wantErr   bool
		errMsg    string
	}{
		{
			name: "single-level path",
			questions: []Question{
				{ID: "db_host", Writes: "db_host"},
			},
			answers: map[string]any{"db_host": "localhost"},
			want: map[string]any{
				"db_host": "localhost",
			},
		},
		{
			name: "nested 3-deep",
			questions: []Question{
				{ID: "app_config_debug", Writes: "app.config.debug"},
			},
			answers: map[string]any{"app_config_debug": true},
			want: map[string]any{
				"app": map[string]any{
					"config": map[string]any{
						"debug": true,
					},
				},
			},
		},
		{
			name: "multiple questions under same parent",
			questions: []Question{
				{ID: "q1", Writes: "app.config.host"},
				{ID: "q2", Writes: "app.config.port"},
			},
			answers: map[string]any{
				"q1": "localhost",
				"q2": 5432,
			},
			want: map[string]any{
				"app": map[string]any{
					"config": map[string]any{
						"host": "localhost",
						"port": 5432,
					},
				},
			},
		},
		{
			name: "skips missing answers",
			questions: []Question{
				{ID: "q1", Writes: "a"},
				{ID: "q2", Writes: "b"},
			},
			answers: map[string]any{"q1": "value1"},
			want: map[string]any{
				"a": "value1",
			},
		},
		{
			name: "empty answers",
			questions: []Question{
				{ID: "q1", Writes: "a"},
			},
			answers: map[string]any{},
			want:    map[string]any{},
		},
		{
			name: "service ports overlay",
			questions: []Question{
				{ID: "web_port", Writes: "services.web.ports.http"},
			},
			answers: map[string]any{"web_port": 8080},
			want: map[string]any{
				"services": map[string]any{
					"web": map[string]any{
						"ports": map[string]any{
							"http": map[string]any{"port": 8080},
						},
					},
				},
			},
		},
		{
			name: "empty path error",
			questions: []Question{
				{ID: "q1", Writes: ""},
			},
			answers: map[string]any{"q1": "value"},
			wantErr: true,
			errMsg:  "empty path",
		},
		{
			name: "empty path segment error",
			questions: []Question{
				{ID: "q1", Writes: "a..b"},
			},
			answers: map[string]any{"q1": "value"},
			wantErr: true,
			errMsg:  "empty path segment",
		},
		{
			name: "collision: non-map to map",
			questions: []Question{
				{ID: "q1", Writes: "app"},
				{ID: "q2", Writes: "app.config"},
			},
			answers: map[string]any{
				"q1": "scalar",
				"q2": "nested",
			},
			wantErr: true,
			errMsg:  "not a map",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildOverlay(tt.questions, tt.answers)
			if tt.wantErr {
				if err == nil {
					t.Errorf("BuildOverlay() error = nil, want non-nil")
				} else if tt.errMsg != "" && !stringContains(err.Error(), tt.errMsg) {
					t.Errorf("BuildOverlay() error = %v, want message containing %q", err, tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("BuildOverlay() error = %v, want nil", err)
				return
			}
			if !mapsEqual(got, tt.want) {
				t.Errorf("BuildOverlay() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildPortOverlay(t *testing.T) {
	tests := []struct {
		name    string
		input   map[PortKey]int
		want    map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name: "single port override",
			input: map[PortKey]int{
				{Service: "web", PortName: "http"}: 8080,
			},
			want: map[string]any{
				"services": map[string]any{
					"web": map[string]any{
						"ports": map[string]any{
							"http": map[string]any{"port": 8080},
						},
					},
				},
			},
		},
		{
			name: "multiple port overrides",
			input: map[PortKey]int{
				{Service: "web", PortName: "http"}:  8080,
				{Service: "web", PortName: "https"}: 8443,
				{Service: "db", PortName: "psql"}:   5433,
			},
			want: map[string]any{
				"services": map[string]any{
					"web": map[string]any{
						"ports": map[string]any{
							"http":  map[string]any{"port": 8080},
							"https": map[string]any{"port": 8443},
						},
					},
					"db": map[string]any{
						"ports": map[string]any{
							"psql": map[string]any{"port": 5433},
						},
					},
				},
			},
		},
		{
			name:  "empty input",
			input: map[PortKey]int{},
			want:  map[string]any{},
		},
		{
			name: "out-of-range: too high",
			input: map[PortKey]int{
				{Service: "web", PortName: "http"}: 99999,
			},
			wantErr: true,
			errMsg:  "out of range",
		},
		{
			name: "out-of-range: zero",
			input: map[PortKey]int{
				{Service: "web", PortName: "http"}: 0,
			},
			wantErr: true,
			errMsg:  "out of range",
		},
		{
			name: "out-of-range: negative",
			input: map[PortKey]int{
				{Service: "web", PortName: "http"}: -1,
			},
			wantErr: true,
			errMsg:  "out of range",
		},
		{
			name: "boundary: port 1",
			input: map[PortKey]int{
				{Service: "web", PortName: "http"}: 1,
			},
			want: map[string]any{
				"services": map[string]any{
					"web": map[string]any{
						"ports": map[string]any{
							"http": map[string]any{"port": 1},
						},
					},
				},
			},
		},
		{
			name: "boundary: port 65535",
			input: map[PortKey]int{
				{Service: "web", PortName: "http"}: 65535,
			},
			want: map[string]any{
				"services": map[string]any{
					"web": map[string]any{
						"ports": map[string]any{
							"http": map[string]any{"port": 65535},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildPortOverlay(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("BuildPortOverlay() error = nil, want non-nil")
				} else if tt.errMsg != "" && !stringContains(err.Error(), tt.errMsg) {
					t.Errorf("BuildPortOverlay() error = %v, want message containing %q", err, tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("BuildPortOverlay() error = %v, want nil", err)
				return
			}
			if !mapsEqual(got, tt.want) {
				t.Errorf("BuildPortOverlay() = %v, want %v", got, tt.want)
			}
		})
	}
}

func stringContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || s[0:len(substr)] == substr || s[len(s)-len(substr):] == substr || contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, exists := b[k]
		if !exists {
			return false
		}
		if !valuesEqual(av, bv) {
			return false
		}
	}
	return true
}

func valuesEqual(a, b any) bool {
	am, aIsMap := a.(map[string]any)
	bm, bIsMap := b.(map[string]any)
	if aIsMap && bIsMap {
		return mapsEqual(am, bm)
	}
	if aIsMap || bIsMap {
		return false
	}
	return a == b
}
