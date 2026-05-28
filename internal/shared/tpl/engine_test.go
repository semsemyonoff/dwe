package tpl

import (
	"testing"
)

type testData struct {
	Name    string
	Enabled bool
	Port    int
}

func TestRender_noTemplate(t *testing.T) {
	got, err := Render("plain text", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "plain text" {
		t.Errorf("Render = %q", got)
	}
}

func TestRender_withTemplate(t *testing.T) {
	data := testData{Name: "laravel"}
	got, err := Render("{{ .Name }}", data)
	if err != nil {
		t.Fatal(err)
	}
	if got != "laravel" {
		t.Errorf("Render = %q", got)
	}
}

func TestRender_invalidTemplate(t *testing.T) {
	_, err := Render("{{ .Name", nil)
	if err == nil {
		t.Error("expected error for unclosed template")
	}
}

func TestEvalCondition_empty(t *testing.T) {
	ok, err := EvalCondition("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("empty when should always be true")
	}
}

func TestEvalCondition_true(t *testing.T) {
	data := testData{Enabled: true}
	ok, err := EvalCondition("{{ .Enabled }}", data)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("Enabled=true should evaluate to true")
	}
}

func TestEvalCondition_false(t *testing.T) {
	data := testData{Enabled: false}
	ok, err := EvalCondition("{{ .Enabled }}", data)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Enabled=false should evaluate to false")
	}
}

func TestEvalCondition_emptyString(t *testing.T) {
	data := testData{Name: ""}
	ok, err := EvalCondition("{{ .Name }}", data)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("empty string should evaluate to false")
	}
}

func TestEvalCondition_nonEmptyString(t *testing.T) {
	data := testData{Name: "staging"}
	ok, err := EvalCondition("{{ .Name }}", data)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("non-empty string should evaluate to true")
	}
}

func TestAppURL_http_defaultPort(t *testing.T) {
	got := appURL("app.localhost", 80, false)
	want := "http://app.localhost"
	if got != want {
		t.Errorf("appURL = %q, want %q", got, want)
	}
}

func TestAppURL_https_defaultPort(t *testing.T) {
	got := appURL("app.localhost", 443, true)
	want := "https://app.localhost"
	if got != want {
		t.Errorf("appURL = %q, want %q", got, want)
	}
}

func TestAppURL_customPort(t *testing.T) {
	got := appURL("app.localhost", 8025, false)
	want := "http://app.localhost:8025"
	if got != want {
		t.Errorf("appURL = %q, want %q", got, want)
	}
}

func TestAppURL_emptyHost(t *testing.T) {
	got := appURL("", 8025, false)
	want := "http://localhost:8025"
	if got != want {
		t.Errorf("appURL empty host = %q, want %q", got, want)
	}
}

func TestAppURL_withPath(t *testing.T) {
	got := appURL("app.localhost", 80, false, "?SPX_KEY=dev")
	want := "http://app.localhost/?SPX_KEY=dev"
	if got != want {
		t.Errorf("appURL with path = %q, want %q", got, want)
	}
}

func TestRender_appURLFunc(t *testing.T) {
	type runtimeData struct {
		Host     string
		Port     int
		UseHTTPS bool
	}
	data := runtimeData{Host: "app.localhost", Port: 80, UseHTTPS: false}
	got, err := Render(`{{ appURL .Host .Port .UseHTTPS }}`, data)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://app.localhost" {
		t.Errorf("Render appURL = %q", got)
	}
}
