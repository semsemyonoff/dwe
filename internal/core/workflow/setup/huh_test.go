package setup

import (
	"errors"
	"fmt"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/validate/env"
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
		{
			name: "optional port preset with blank answer is omitted",
			questions: []Question{
				{
					ID:       "port_input",
					Type:     TypeInput,
					Required: false,
					Validate: &ValidateSpec{Preset: PresetPort},
				},
			},
			raws: map[string]string{
				"port_input": "",
			},
			want: map[string]any{}, // blank optional → not stored
		},
		{
			name: "optional hostname preset with blank answer is omitted",
			questions: []Question{
				{
					ID:       "hostname_input",
					Type:     TypeInput,
					Required: false,
					Validate: &ValidateSpec{Preset: PresetHostname},
				},
			},
			raws: map[string]string{
				"hostname_input": "   ",
			},
			want: map[string]any{}, // whitespace-only optional → not stored
		},
		{
			name: "optional regex input with blank answer is omitted",
			questions: []Question{
				{
					ID:       "regex_input",
					Type:     TypeInput,
					Required: false,
					Validate: &ValidateSpec{Regex: "^[a-z]+$"},
				},
			},
			raws: map[string]string{
				"regex_input": "",
			},
			want: map[string]any{}, // blank optional → not stored
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

// Test mapWizardCancel: widgets.ErrCancelled maps to ErrWizardCanceled;
// every other error is wrapped, not swallowed.
func TestMapWizardCancel(t *testing.T) {
	if err := mapWizardCancel(widgets.ErrCancelled); !errors.Is(err, ErrWizardCanceled) {
		t.Errorf("mapWizardCancel(ErrCancelled) = %v, want ErrWizardCanceled", err)
	}

	other := errors.New("boom")
	err := mapWizardCancel(other)
	if !errors.Is(err, other) {
		t.Errorf("mapWizardCancel(other) = %v, want wrapped %v", err, other)
	}
	if errors.Is(err, ErrWizardCanceled) {
		t.Errorf("mapWizardCancel(other) unexpectedly maps to ErrWizardCanceled")
	}
}

// Test buildPortOverrideFields: one FieldInput per conflict, keyed
// "service/portName", defaulted to the requested port.
func TestBuildPortOverrideFields(t *testing.T) {
	conflicts := []env.PortConflict{
		{Service: "web", PortName: "http", RequestedPort: 8080, OccupiedBy: "nginx"},
		{Service: "api", PortName: "grpc", RequestedPort: 50051, OccupiedBy: "other"},
	}

	fields := buildPortOverrideFields(conflicts)
	if len(fields) != len(conflicts) {
		t.Fatalf("len(fields) = %d, want %d", len(fields), len(conflicts))
	}

	for i, conflict := range conflicts {
		f := fields[i]
		wantKey := fmt.Sprintf("%s/%s", conflict.Service, conflict.PortName)
		if f.Key != wantKey {
			t.Errorf("fields[%d].Key = %q, want %q", i, f.Key, wantKey)
		}
		if f.Kind != ask.FieldInput {
			t.Errorf("fields[%d].Kind = %v, want FieldInput", i, f.Kind)
		}
		if f.Default != fmt.Sprintf("%d", conflict.RequestedPort) {
			t.Errorf("fields[%d].Default = %q, want %d", i, f.Default, conflict.RequestedPort)
		}
		if f.Validate == nil {
			t.Errorf("fields[%d].Validate is nil, want buildPortValidator()", i)
		}
	}
}

// Test buildPortOverrideFields with zero conflicts returns an empty slice.
func TestBuildPortOverrideFieldsEmpty(t *testing.T) {
	fields := buildPortOverrideFields(nil)
	if len(fields) != 0 {
		t.Errorf("len(fields) = %d, want 0", len(fields))
	}
}

// Test portOverrideRaws: harvests each conflict's answer from an ask.Result
// keyed the same way buildPortOverrideFields keys its fields.
func TestPortOverrideRaws(t *testing.T) {
	conflicts := []env.PortConflict{
		{Service: "web", PortName: "http", RequestedPort: 8080, OccupiedBy: "nginx"},
		{Service: "api", PortName: "grpc", RequestedPort: 50051, OccupiedBy: "other"},
	}
	result := ask.NewResultForTest(map[string]any{
		"web/http": "9000",
		"api/grpc": "60000",
	})

	raws := portOverrideRaws(conflicts, result)
	want := map[string]string{"web/http": "9000", "api/grpc": "60000"}
	if len(raws) != len(want) {
		t.Fatalf("len(raws) = %d, want %d", len(raws), len(want))
	}
	for k, v := range want {
		if raws[k] != v {
			t.Errorf("raws[%q] = %q, want %q", k, raws[k], v)
		}
	}
}

// Test buildServiceTogglesField: mandatory rows are excluded from the field
// and reported as labels; optional rows become options with the enabled
// ones preselected via Defaults; Filterable is pinned to false.
func TestBuildServiceTogglesField(t *testing.T) {
	toggles := []ServiceToggle{
		{Name: "db", Type: "database", Mandatory: true, Enabled: true},
		{Name: "web", Type: "app", Mandatory: false, Enabled: true},
		{Name: "cache", Type: "cache", Mandatory: false, Enabled: false},
	}

	field, mandatoryLabels := buildServiceTogglesField(toggles)
	if field == nil {
		t.Fatal("buildServiceTogglesField() field is nil, want non-nil (mixed mandatory/optional)")
	}
	if len(mandatoryLabels) != 1 {
		t.Errorf("len(mandatoryLabels) = %d, want 1", len(mandatoryLabels))
	}

	if field.Kind != ask.FieldMultiselect {
		t.Errorf("field.Kind = %v, want FieldMultiselect", field.Kind)
	}
	if len(field.Options) != 2 {
		t.Errorf("len(field.Options) = %d, want 2 (db excluded)", len(field.Options))
	}
	for _, opt := range field.Options {
		if opt.Value == "db" {
			t.Errorf("field.Options contains mandatory service %q", opt.Value)
		}
	}
	if len(field.Defaults) != 1 || field.Defaults[0] != "web" {
		t.Errorf("field.Defaults = %v, want [web]", field.Defaults)
	}
	if field.Filterable == nil || *field.Filterable != false {
		t.Errorf("field.Filterable = %v, want pointer to false", field.Filterable)
	}
}

// Test buildServiceTogglesField with every toggle mandatory: nil field,
// every row reported as a mandatory label.
func TestBuildServiceTogglesFieldAllMandatory(t *testing.T) {
	toggles := []ServiceToggle{
		{Name: "db", Mandatory: true},
		{Name: "cache", Mandatory: true},
	}

	field, mandatoryLabels := buildServiceTogglesField(toggles)
	if field != nil {
		t.Errorf("buildServiceTogglesField() field = %+v, want nil (all mandatory)", field)
	}
	if len(mandatoryLabels) != 2 {
		t.Errorf("len(mandatoryLabels) = %d, want 2", len(mandatoryLabels))
	}
}

// Test mandatoryToggles: only mandatory services are reported as kept.
func TestMandatoryToggles(t *testing.T) {
	toggles := []ServiceToggle{
		{Name: "db", Mandatory: true},
		{Name: "cache", Mandatory: true},
		{Name: "web", Mandatory: false},
	}

	got := mandatoryToggles(toggles)
	want := map[string]bool{"db": true, "cache": true}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %v, want %v", k, got[k], v)
		}
	}
}

// Test mergeMandatoryToggles: user picks plus mandatory services merge into
// one map; mandatory services are kept even when not in picked.
func TestMergeMandatoryToggles(t *testing.T) {
	toggles := []ServiceToggle{
		{Name: "db", Mandatory: true},
		{Name: "web", Mandatory: false},
		{Name: "cache", Mandatory: false},
	}
	picked := []string{"web"}

	got := mergeMandatoryToggles(picked, toggles)
	want := map[string]bool{"db": true, "web": true}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %v, want %v", k, got[k], v)
		}
	}
	if got["cache"] {
		t.Errorf("got[cache] = true, want false (not picked, not mandatory)")
	}
}
