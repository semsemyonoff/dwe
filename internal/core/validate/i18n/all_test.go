package i18n

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/registry"
	"github.com/semsemyonoff/devbox/internal/core/validate"
	"github.com/semsemyonoff/devbox/internal/shared/i18n"
)

func TestParseError(t *testing.T) {
	// Per-file parse error
	pf := i18n.ProjectFile{
		Path:     "/project/devbox/i18n/ru.yml",
		Locale:   "ru",
		ParseErr: errors.New("unknown field: description"),
	}

	validators := All([]i18n.ProjectFile{pf}, nil)
	if len(validators) != 1 {
		t.Fatalf("expected 1 validator, got %d", len(validators))
	}

	diags := validators[0].Run(validate.Context{})
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	d := diags[0]
	if d.Severity != validate.SeverityError {
		t.Errorf("expected SeverityError, got %v", d.Severity)
	}
	if d.Domain != "i18n" {
		t.Errorf("expected domain 'i18n', got %q", d.Domain)
	}
	if d.File != "/project/devbox/i18n/ru.yml" {
		t.Errorf("expected file path in diagnostic, got %q", d.File)
	}
	if !strings.Contains(d.Message, "unknown field") {
		t.Errorf("expected message to mention parse error, got %q", d.Message)
	}
}

func TestOrphanCommand(t *testing.T) {
	// Create a registry with one command
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:        "db.migrate",
		Group:     "db",
		LocalName: "migrate",
		Type:      model.CommandTypeShell,
	})

	// Create a translation file with an orphaned command
	pf := i18n.ProjectFile{
		Path:   "/project/devbox/i18n/ru.yml",
		Locale: "ru",
		Bundle: &i18n.Bundle{
			Commands: map[string]i18n.CommandStrings{
				"db.migrate": {Description: "Migrate"},
				"app.build":  {Description: "Build"}, // orphaned
			},
		},
	}

	validators := All([]i18n.ProjectFile{pf}, reg)

	// Find the orphan validator
	var orphanVal validate.Validator
	for _, v := range validators {
		if _, ok := v.(*orphanValidator); ok {
			orphanVal = v
			break
		}
	}

	if orphanVal == nil {
		t.Fatal("orphan validator not created")
	}

	diags := orphanVal.Run(validate.Context{})
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for orphaned command, got %d", len(diags))
	}

	d := diags[0]
	if d.Severity != validate.SeverityWarning {
		t.Errorf("expected SeverityWarning, got %v", d.Severity)
	}
	if !strings.Contains(d.Message, "app.build") {
		t.Errorf("expected message to mention 'app.build', got %q", d.Message)
	}
}

func TestOrphanGroup(t *testing.T) {
	// Create a registry with one group containing a command
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:        "db.migrate",
		Group:     "db",
		LocalName: "migrate",
		Type:      model.CommandTypeShell,
	})

	// Create a translation file with an orphaned group
	pf := i18n.ProjectFile{
		Path:   "/project/devbox/i18n/ru.yml",
		Locale: "ru",
		Bundle: &i18n.Bundle{
			Groups: map[string]i18n.GroupStrings{
				"db":  {Description: "Database"},
				"app": {Description: "Application"}, // orphaned
			},
		},
	}

	validators := All([]i18n.ProjectFile{pf}, reg)

	var orphanVal validate.Validator
	for _, v := range validators {
		if _, ok := v.(*orphanValidator); ok {
			orphanVal = v
			break
		}
	}

	if orphanVal == nil {
		t.Fatal("orphan validator not created")
	}

	diags := orphanVal.Run(validate.Context{})
	if len(diags) != 1 {
		for i, d := range diags {
			t.Logf("diagnostic %d: target=%q message=%q", i, d.Target, d.Message)
		}
		t.Fatalf("expected 1 diagnostic for orphaned group, got %d", len(diags))
	}

	d := diags[0]
	if d.Severity != validate.SeverityWarning {
		t.Errorf("expected SeverityWarning, got %v", d.Severity)
	}
	if !strings.Contains(d.Message, "app") {
		t.Errorf("expected message to mention 'app', got %q", d.Message)
	}
}

func TestUnknownUIKey(t *testing.T) {
	pf := i18n.ProjectFile{
		Path:   "/project/devbox/i18n/ru.yml",
		Locale: "ru",
		Bundle: &i18n.Bundle{
			UI: map[string]string{
				"docs.section.properties": "Свойства",
				"docs.section.properies":  "Опечатка", // typo'd key
				"docs.property.custom":    "Custom",   // unknown key
			},
		},
	}

	validators := All([]i18n.ProjectFile{pf}, nil)

	var unknownVal validate.Validator
	for _, v := range validators {
		if _, ok := v.(*unknownUIKeyValidator); ok {
			unknownVal = v
			break
		}
	}

	if unknownVal == nil {
		t.Fatal("unknown ui key validator not created")
	}

	diags := unknownVal.Run(validate.Context{})
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics for unknown keys, got %d", len(diags))
	}

	for _, d := range diags {
		if d.Severity != validate.SeverityWarning {
			t.Errorf("expected SeverityWarning, got %v", d.Severity)
		}
	}
}

func TestEmptyProject(t *testing.T) {
	// No translation files -> no validators
	validators := All(nil, nil)
	if len(validators) != 0 {
		t.Fatalf("expected 0 validators for empty project, got %d", len(validators))
	}
}

func TestValidTranslation(t *testing.T) {
	// Create a registry with a command
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:        "db.migrate",
		Group:     "db",
		LocalName: "migrate",
		Type:      model.CommandTypeShell,
	})

	// Valid translation file
	pf := i18n.ProjectFile{
		Path:   "/project/devbox/i18n/ru.yml",
		Locale: "ru",
		Bundle: &i18n.Bundle{
			UI: map[string]string{
				"docs.section.properties": "Свойства",
			},
			Commands: map[string]i18n.CommandStrings{
				"db.migrate": {Description: "Мигрировать"},
			},
		},
	}

	validators := All([]i18n.ProjectFile{pf}, reg)

	// Should have validators for orphan and unknown ui key checks, but no diagnostics
	var orphanVal, unknownVal validate.Validator
	for _, v := range validators {
		if _, ok := v.(*orphanValidator); ok {
			orphanVal = v
		}
		if _, ok := v.(*unknownUIKeyValidator); ok {
			unknownVal = v
		}
	}

	if orphanVal != nil {
		diags := orphanVal.Run(validate.Context{})
		if len(diags) != 0 {
			t.Errorf("expected 0 orphan diagnostics, got %d", len(diags))
		}
	}

	if unknownVal != nil {
		diags := unknownVal.Run(validate.Context{})
		if len(diags) != 0 {
			t.Errorf("expected 0 unknown key diagnostics, got %d", len(diags))
		}
	}
}

func TestDirectoryLevelLoadError(t *testing.T) {
	// Sentinel ProjectFile with Locale == "" indicates directory-level failure
	pf := i18n.ProjectFile{
		Path:     "/project/devbox/i18n",
		Locale:   "",
		ParseErr: os.ErrPermission,
	}

	validators := All([]i18n.ProjectFile{pf}, nil)

	// Find the parse error validator
	var parseErrVal validate.Validator
	for _, v := range validators {
		if _, ok := v.(*parseErrorValidator); ok {
			parseErrVal = v
			break
		}
	}

	if parseErrVal == nil {
		t.Fatal("parse error validator not created")
	}

	// Check that it implements DomainLevelValidator
	if dlv, ok := parseErrVal.(validate.DomainLevelValidator); !ok {
		t.Fatal("parse error validator does not implement DomainLevelValidator")
	} else if !dlv.IsDomainLevel() {
		t.Error("expected IsDomainLevel() == true for directory-level failure")
	}

	diags := parseErrVal.Run(validate.Context{})
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	if diags[0].Severity != validate.SeverityError {
		t.Errorf("expected SeverityError, got %v", diags[0].Severity)
	}
}

func TestParseErrorLocaleField(t *testing.T) {
	pf := i18n.ProjectFile{
		Path:     "/project/devbox/i18n/ru.yml",
		Locale:   "ru",
		ParseErr: errors.New("unknown field: bad_key"),
	}

	validators := All([]i18n.ProjectFile{pf}, nil)
	pev := validators[0].(*parseErrorValidator)

	if pev.pf.Locale != "ru" {
		t.Errorf("expected locale 'ru', got %q", pev.pf.Locale)
	}
}
