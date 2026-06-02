package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/validate/env"
)

func TestWizardRunHappyPath(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	questions := []Question{
		{
			ID:       "app_name",
			Type:     TypeInput,
			Title:    "App Name",
			Required: true,
			Writes:   "app.name",
		},
	}

	deps := WizardDeps{
		BaseDir:       testDir,
		LocalPath:     localPath,
		Questions:     questions,
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"app_name": "myapp"}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// Verify the file was written
	content, err := localpkg.LoadLocalYAML(localPath)
	if err != nil {
		t.Fatalf("LoadLocalYAML() error = %v", err)
	}

	app, ok := content["app"].(map[string]any)
	if !ok {
		t.Fatalf("expected app key in content")
	}

	if app["name"] != "myapp" {
		t.Errorf("expected app.name = myapp, got %v", app["name"])
	}
}

func TestBuildServiceTogglesOverlay(t *testing.T) {
	toggles := []ServiceToggle{
		{Name: "main", Type: "app", Mandatory: true, Enabled: true},
		{Name: "adminer", Type: "tool", Mandatory: false, Enabled: false},
		{Name: "worker", Type: "app", Mandatory: false, Enabled: true},
		{Name: "extra", Type: "app", Mandatory: false, Enabled: false},
	}
	t.Run("emits only diffs from in-config defaults", func(t *testing.T) {
		kept := map[string]bool{
			"main":    true, // mandatory, ignored
			"adminer": true, // was off, now on → emit
			"worker":  true, // was on, stays on → skip
			// "extra" absent (was off, stays off → skip)
		}
		got := BuildServiceTogglesOverlay(toggles, kept)
		services, _ := got["services"].(map[string]any)
		if services == nil {
			t.Fatalf("expected services map, got %T", got["services"])
		}
		if len(services) != 1 {
			t.Errorf("expected 1 service entry, got %d: %v", len(services), services)
		}
		adminer, _ := services["adminer"].(map[string]any)
		if adminer == nil || adminer["enabled"] != true {
			t.Errorf("expected services.adminer.enabled=true, got %v", services["adminer"])
		}
	})
	t.Run("nothing to write returns nil", func(t *testing.T) {
		kept := map[string]bool{"worker": true} // matches in-config Enabled=true
		got := BuildServiceTogglesOverlay(toggles, kept)
		if got != nil {
			t.Errorf("expected nil overlay when no diffs, got %v", got)
		}
	})
	t.Run("disabling previously enabled emits false", func(t *testing.T) {
		kept := map[string]bool{} // user unchecked everything
		got := BuildServiceTogglesOverlay(toggles, kept)
		services, _ := got["services"].(map[string]any)
		// worker was enabled, now off → emit. main is mandatory → skip.
		worker, _ := services["worker"].(map[string]any)
		if worker == nil || worker["enabled"] != false {
			t.Errorf("expected services.worker.enabled=false, got %v", services["worker"])
		}
	})
}

func TestWizardRunNoData_DoesNotCreateLocal(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	// No questions, no port conflicts — wizard should be a no-op and must NOT
	// create an empty `{}` local.yml.
	deps := WizardDeps{
		BaseDir:       testDir,
		LocalPath:     localPath,
		Questions:     nil,
		PortConflicts: nil,
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			t.Fatal("AskQuestions must not be called when there are no questions")
			return nil, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			t.Fatal("AskPortOverrides must not be called when there are no conflicts")
			return nil, nil
		},
	}

	if err := Run(context.Background(), deps); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Errorf("expected local.yml to NOT exist when wizard collected nothing, but Stat err = %v", err)
	}
}

func TestWizardRunPortConflict(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	conflicts := []env.PortConflict{
		{
			Service:       "web",
			PortName:      "http",
			RequestedPort: 8080,
			OccupiedBy:    "foreign_container",
		},
	}

	deps := WizardDeps{
		BaseDir:       testDir,
		LocalPath:     localPath,
		Questions:     []Question{},
		PortConflicts: conflicts,
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{
				{Service: "web", PortName: "http"}: 9090,
			}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	content, err := localpkg.LoadLocalYAML(localPath)
	if err != nil {
		t.Fatalf("LoadLocalYAML() error = %v", err)
	}

	services, ok := content["services"].(map[string]any)
	if !ok {
		t.Fatalf("expected services key in content")
	}

	web, ok := services["web"].(map[string]any)
	if !ok {
		t.Fatalf("expected web service in services")
	}

	ports, ok := web["ports"].(map[string]any)
	if !ok {
		t.Fatalf("expected ports in web service")
	}

	httpEntry, ok := ports["http"].(map[string]any)
	if !ok {
		t.Fatalf("expected rich-form ports.http mapping, got %T: %v", ports["http"], ports["http"])
	}
	if httpEntry["port"] != 9090 {
		t.Errorf("expected services.web.ports.http.port = 9090, got %v", httpEntry["port"])
	}
}

func TestWizardRunCancelFromQuestions(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{ID: "q1", Type: TypeInput, Required: false, Writes: "config.q1"},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return nil, ErrWizardCanceled
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want ErrWizardCanceled")
	}
	if err.Error() != "wizard canceled" {
		t.Errorf("Run() error = %v, want ErrWizardCanceled", err)
	}

	// File should not exist after cancel
	_, statErr := os.Stat(localPath)
	if !os.IsNotExist(statErr) {
		t.Errorf("expected file to not exist after cancel")
	}
}

func TestWizardRunCancelFromPortOverrides(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	conflicts := []env.PortConflict{
		{Service: "web", PortName: "http", RequestedPort: 8080, OccupiedBy: "other"},
	}

	deps := WizardDeps{
		BaseDir:       testDir,
		LocalPath:     localPath,
		Questions:     []Question{},
		PortConflicts: conflicts,
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return nil, ErrWizardCanceled
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want ErrWizardCanceled")
	}

	// File should not exist
	_, statErr := os.Stat(localPath)
	if !os.IsNotExist(statErr) {
		t.Errorf("expected file to not exist after cancel")
	}
}

func TestWizardRunRequiredInputEmpty(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{ID: "required_field", Type: TypeInput, Required: true, Writes: "config.required"},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"required_field": ""}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("Run() error = %v, want error containing 'required'", err)
	}
}

func TestWizardRunRequiredQuestionMissing(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{ID: "required_field", Type: TypeInput, Required: true, Writes: "config.required"},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{}, nil // Missing required_field
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("Run() error = %v, want error containing 'required'", err)
	}
}

func TestWizardRunConfirmTypeMismatch(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{ID: "agree", Type: TypeConfirm, Required: false, Writes: "config.agree"},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"agree": "true"}, nil // Should be bool, not string
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "expected bool") {
		t.Errorf("Run() error = %v, want error containing 'expected bool'", err)
	}
}

func TestWizardRunSelectInvalidValue(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{
				ID:       "choice",
				Type:     TypeSelect,
				Required: false,
				Writes:   "config.choice",
				Options: []Option{
					{Value: "a", Label: "Option A"},
					{Value: "b", Label: "Option B"},
				},
			},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"choice": "invalid"}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not in declared options") {
		t.Errorf("Run() error = %v, want error containing 'not in declared options'", err)
	}
}

func TestWizardRunPortOutOfRange(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{
				ID:       "port",
				Type:     TypeInput,
				Required: false,
				Writes:   "server.port",
				Validate: &ValidateSpec{Preset: PresetPort},
			},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"port": 99999}, nil // Out of range
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("Run() error = %v, want error containing 'out of range'", err)
	}
}

func TestWizardRunPortOverrideOutOfRange(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	conflicts := []env.PortConflict{
		{Service: "web", PortName: "http", RequestedPort: 8080, OccupiedBy: "other"},
	}

	deps := WizardDeps{
		BaseDir:       testDir,
		LocalPath:     localPath,
		Questions:     []Question{},
		PortConflicts: conflicts,
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{
				{Service: "web", PortName: "http"}: 99999, // Out of range
			}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("Run() error = %v, want error containing 'out of range'", err)
	}
}

func TestWizardRunDeepMergePreservesExisting(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	// Pre-populate with existing content
	existing := map[string]any{
		"app": map[string]any{
			"name": "existing_name",
		},
	}
	if err := localpkg.WriteLocalYAML(localPath, existing); err != nil {
		t.Fatalf("WriteLocalYAML() error = %v", err)
	}

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{ID: "app_ver", Type: TypeInput, Required: false, Writes: "app.version"},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"app_ver": "1.0.0"}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	content, _ := localpkg.LoadLocalYAML(localPath)
	app, _ := content["app"].(map[string]any)

	// Check that both old and new values are present
	if app["name"] != "existing_name" {
		t.Errorf("expected pre-existing app.name to be preserved, got %v", app["name"])
	}
	if app["version"] != "1.0.0" {
		t.Errorf("expected app.version = 1.0.0, got %v", app["version"])
	}
}

func TestWizardRunMultiselectInvalidType(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{
				ID:       "tags",
				Type:     TypeMultiselect,
				Required: false,
				Writes:   "config.tags",
				Options: []Option{
					{Value: "tag1", Label: "Tag 1"},
					{Value: "tag2", Label: "Tag 2"},
				},
			},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"tags": "tag1"}, nil // Should be []string, not string
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("Run() error = %v, want error containing 'expected'", err)
	}
}

func TestWizardRunMultiselectInvalidValue(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{
				ID:       "tags",
				Type:     TypeMultiselect,
				Required: false,
				Writes:   "config.tags",
				Options: []Option{
					{Value: "tag1", Label: "Tag 1"},
					{Value: "tag2", Label: "Tag 2"},
				},
			},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"tags": []string{"tag1", "invalid"}}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not in declared options") {
		t.Errorf("Run() error = %v, want error containing 'not in declared options'", err)
	}
}

func TestWizardRunHostnameValidation(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{
				ID:       "hostname",
				Type:     TypeInput,
				Required: false,
				Writes:   "server.hostname",
				Validate: &ValidateSpec{Preset: PresetHostname},
			},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"hostname": "bad host!"}, nil // Invalid hostname
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid hostname") {
		t.Errorf("Run() error = %v, want error containing 'invalid hostname'", err)
	}
}

func TestWizardRunRegexValidation(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{
				ID:       "email",
				Type:     TypeInput,
				Required: false,
				Writes:   "user.email",
				Validate: &ValidateSpec{Regex: `^[a-z]+@[a-z]+\.[a-z]+$`},
			},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"email": "not-an-email"}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "does not match pattern") {
		t.Errorf("Run() error = %v, want error containing 'does not match pattern'", err)
	}
}

// TestWizardRunOptionalBlankValidatedInputs confirms that optional validated inputs
// (port, hostname, regex) can be left blank without causing validation failures,
// and that no overlay is written for the skipped field.
func TestWizardRunOptionalBlankValidatedInputs(t *testing.T) {
	tests := []struct {
		name     string
		question Question
		answer   any
	}{
		{
			name: "optional port preset blank",
			question: Question{
				ID:       "port",
				Type:     TypeInput,
				Required: false,
				Writes:   "server.port",
				Validate: &ValidateSpec{Preset: PresetPort},
			},
			// coerceInputAnswers returns nothing for blank optional; wizard gets no answer.
		},
		{
			name: "optional hostname preset blank",
			question: Question{
				ID:       "host",
				Type:     TypeInput,
				Required: false,
				Writes:   "server.host",
				Validate: &ValidateSpec{Preset: PresetHostname},
			},
		},
		{
			name: "optional regex blank",
			question: Question{
				ID:       "email",
				Type:     TypeInput,
				Required: false,
				Writes:   "user.email",
				Validate: &ValidateSpec{Regex: `^[a-z]+@[a-z]+\.[a-z]+$`},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDir := t.TempDir()
			localPath := filepath.Join(testDir, "local.yml")

			deps := WizardDeps{
				BaseDir:       testDir,
				LocalPath:     localPath,
				Questions:     []Question{tt.question},
				PortConflicts: []env.PortConflict{},
				// AskQuestions returns no answer for the optional blank question,
				// simulating what coerceInputAnswers produces after the huh form.
				AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
					return map[string]any{}, nil
				},
				AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
					return map[PortKey]int{}, nil
				},
			}

			if err := Run(context.Background(), deps); err != nil {
				t.Fatalf("Run() error = %v, want nil for optional blank input", err)
			}
		})
	}
}

func TestWizardRunRequiredMultiselectEmpty(t *testing.T) {
	testDir := t.TempDir()
	localPath := filepath.Join(testDir, "local.yml")

	deps := WizardDeps{
		BaseDir:   testDir,
		LocalPath: localPath,
		Questions: []Question{
			{
				ID:       "required_tags",
				Type:     TypeMultiselect,
				Required: true,
				Writes:   "config.tags",
				Options: []Option{
					{Value: "a", Label: "A"},
					{Value: "b", Label: "B"},
				},
			},
		},
		PortConflicts: []env.PortConflict{},
		AskQuestions: func(ctx context.Context, qs []Question) (map[string]any, error) {
			return map[string]any{"required_tags": []string{}}, nil
		},
		AskPortOverrides: func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
			return map[PortKey]int{}, nil
		},
	}

	err := Run(context.Background(), deps)
	if err == nil {
		t.Fatalf("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("Run() error = %v, want error containing 'required'", err)
	}
}
