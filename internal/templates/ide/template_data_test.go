package ide

import (
	"bytes"
	"strings"
	"testing"
	"text/template"

	"devbox-cli/internal/config"
)

func sampleServices() map[string]config.ServiceConfig {
	return map[string]config.ServiceConfig{
		"main": {
			Type:  config.ServiceTypeApp,
			Ports: map[string]int{"http": 8080, "grpc": 9090},
			Hosts: map[string]string{"web": "main.localhost"},
		},
		"app2": {Type: config.ServiceTypeApp},
		"db": {
			Type:  config.ServiceTypeInfra,
			Ports: map[string]int{"sql": 3306},
		},
		"adminer": {
			Type:  config.ServiceTypeTool,
			Hosts: map[string]string{"web": "adminer.localhost"},
		},
	}
}

func TestTemplateData_filterAccessors(t *testing.T) {
	d := TemplateData{Services: sampleServices()}

	if got := d.AppServices(); len(got) != 2 || got["main"].Type != config.ServiceTypeApp {
		t.Errorf("AppServices = %+v, want 2 app entries", got)
	}
	if got := d.ToolServices(); len(got) != 1 || got["adminer"].Type != config.ServiceTypeTool {
		t.Errorf("ToolServices = %+v, want adminer", got)
	}
	if got := d.InfraServices(); len(got) != 1 || got["db"].Type != config.ServiceTypeInfra {
		t.Errorf("InfraServices = %+v, want db", got)
	}
}

func TestTemplateData_renderAccessors(t *testing.T) {
	d := TemplateData{Services: sampleServices()}

	src := `tools={{ range $name, $_ := .ToolServices }}{{ $name }},{{ end }}` +
		`port={{ (index .Services "main").Port "http" }};` +
		`host={{ (index .Services "main").Host "web" }};` +
		`zero={{ (index .Services "main").Port "missing" }};` +
		`apps={{ len .AppServices }};infra={{ len .InfraServices }}`

	tmpl, err := template.New("t").Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"tools=adminer,",
		"port=8080",
		"host=main.localhost",
		"zero=0",
		"apps=2",
		"infra=1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q; got %q", want, got)
		}
	}
}
