package tpl

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// Render evaluates a Go template string against data, returning the rendered text.
// If expr contains no template markers it is returned as-is.
func Render(expr string, data any) (string, error) {
	if !strings.Contains(expr, "{{") {
		return expr, nil
	}
	t, err := template.New("").Funcs(FuncMap()).Parse(expr)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", expr, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", expr, err)
	}
	return buf.String(), nil
}

// EvalCondition evaluates a when expression against data.
// Empty expr always returns true (show the item).
// The item is hidden when the rendered result is "", "false", or "0".
func EvalCondition(expr string, data any) (bool, error) {
	if expr == "" {
		return true, nil
	}
	result, err := Render(expr, data)
	if err != nil {
		return false, err
	}
	result = strings.TrimSpace(result)
	return result != "" && result != "false" && result != "0", nil
}
