package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

func makeInfoConfig(sections []config.InfoSection) *config.InfoConfig {
	return &config.InfoConfig{Sections: sections}
}

func TestRenderInfo_SectionTitle(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{ID: "s1", Title: "Project Info", Items: nil},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Project Info") {
		t.Errorf("expected section title in output, got:\n%s", out)
	}
}

func TestRenderInfo_DefinitionItem(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID:    "s1",
			Title: "Details",
			Items: []config.InfoItem{
				{Type: "definition", Name: "project", Value: "myapp"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "project") {
		t.Errorf("expected definition key in output, got:\n%s", out)
	}
	if !strings.Contains(out, "myapp") {
		t.Errorf("expected definition value in output, got:\n%s", out)
	}
}

func TestRenderInfo_DefinitionWithIcon(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "url", Value: "http://app.local", Icon: "🌐"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "🌐") {
		t.Errorf("expected icon in output, got:\n%s", out)
	}
	if !strings.Contains(out, "http://app.local") {
		t.Errorf("expected value in output, got:\n%s", out)
	}
}

func TestRenderInfo_WarningItem(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "warning", Text: "something is wrong"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "something is wrong") {
		t.Errorf("expected warning text in output, got:\n%s", out)
	}
}

func TestRenderInfo_InfoItem(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "info", Text: "helpful hint"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "helpful hint") {
		t.Errorf("expected info text in output, got:\n%s", out)
	}
}

func TestRenderInfo_ConditionalItem_Show(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "env", Value: "prod", When: "{{if .Project.Name}}true{{end}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "prod") {
		t.Errorf("expected conditional item shown, got:\n%s", out)
	}
}

func TestRenderInfo_ConditionalItem_Hide(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "env", Value: "prod", When: "{{if .Project.Name}}true{{end}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: ""},
	}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "prod") {
		t.Errorf("expected conditional item hidden, got:\n%s", out)
	}
}

func TestRenderInfo_TemplateInValue(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "name", Value: "{{.Project.Name}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "testproject"},
	}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "testproject") {
		t.Errorf("expected template value evaluated, got:\n%s", out)
	}
}

func TestRenderInfo_UnknownItemType_Ignored(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "unknown_future_type", Text: "ignored"},
				{Type: "definition", Name: "key", Value: "val"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unknown type is skipped, but known type still appears.
	if !strings.Contains(out, "val") {
		t.Errorf("expected known item rendered, got:\n%s", out)
	}
}

func TestRenderInfo_Footer(t *testing.T) {
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{ID: "s1", Title: "Test"},
		},
		Footer: true,
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Footer adds a separator line. The output should have multiple lines.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected multiple lines with footer, got:\n%s", out)
	}
}

func TestRenderInfo_MultipleSection(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{ID: "s1", Title: "First"},
		{ID: "s2", Title: "Second"},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "First") || !strings.Contains(out, "Second") {
		t.Errorf("expected both section titles, got:\n%s", out)
	}
}

func TestRenderInfo_TemplateError(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "k", Value: "{{.Invalid.Field.Path}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	_, err := RenderInfo(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from invalid template, got nil")
	}
}
