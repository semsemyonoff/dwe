// Package condition evaluates deploy step when-expressions at runtime.
//
// Three expression kinds are supported:
//
//   - Go template  — contains "{{"; evaluated against DevboxConfig at plan-resolution time
//   - Builtin predicate — e.g. "dir-empty services/main/src"; evaluated at step-execution time
//   - Shell command — prefixed "cmd: "; evaluated at step-execution time via sh -c
//
// IsRuntime reports whether an expression requires runtime (execution-time) evaluation.
// Template expressions are handled by the tpl package, not here.
package condition

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind classifies a when expression.
type Kind int

const (
	// KindTemplate is a Go template expression (contains "{{").
	// Evaluated against DevboxConfig at plan-resolution time by the tpl package.
	KindTemplate Kind = iota

	// KindBuiltin is a filesystem predicate ("dir-exists", "dir-missing", etc.).
	// Evaluated at step-execution time by EvalBuiltin.
	KindBuiltin

	// KindCmd is a shell command prefixed with "cmd:".
	// Evaluated at step-execution time by EvalCmd.
	KindCmd
)

// Classify determines the Kind of a when expression and returns the
// normalised payload (template expr, predicate string, or command string).
// Empty expr returns KindTemplate with empty payload (always-true by convention).
func Classify(expr string) (Kind, string) {
	expr = strings.TrimSpace(expr)
	if expr == "" || strings.Contains(expr, "{{") {
		return KindTemplate, expr
	}
	if strings.HasPrefix(expr, "cmd:") {
		return KindCmd, strings.TrimSpace(expr[len("cmd:"):])
	}
	return KindBuiltin, expr
}

// IsRuntime reports whether expr requires evaluation at step-execution time
// (KindBuiltin or KindCmd). Go template expressions and empty strings return false.
func IsRuntime(expr string) bool {
	kind, _ := Classify(expr)
	return kind == KindBuiltin || kind == KindCmd
}

// EvalBuiltin evaluates a builtin predicate string against the filesystem.
// projectRoot is used as the base for relative paths.
//
// Supported predicates:
//
//	dir-exists   <path>   — true if path is an existing directory
//	dir-missing  <path>   — true if path does not exist or is not a directory
//	dir-empty    <path>   — true if path does not exist or is an empty directory
//	dir-not-empty <path>  — true if path is a directory with at least one entry
//	file-exists  <path>   — true if path is an existing file
//	file-missing <path>   — true if path does not exist or is not a regular file
func EvalBuiltin(predicate, projectRoot string) (bool, error) {
	predicate = strings.TrimSpace(predicate)
	parts := strings.SplitN(predicate, " ", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return false, fmt.Errorf("builtin predicate %q: expected \"<verb> <path>\"", predicate)
	}
	verb := strings.TrimSpace(parts[0])
	rel := strings.TrimSpace(parts[1])
	path := rel
	if !filepath.IsAbs(rel) {
		path = filepath.Join(projectRoot, rel)
	}

	switch verb {
	case "dir-exists":
		return isDirExisting(path), nil
	case "dir-missing":
		return !isDirExisting(path), nil
	case "dir-empty":
		return isDirEmpty(path)
	case "dir-not-empty":
		empty, err := isDirEmpty(path)
		return !empty, err
	case "file-exists":
		return isFileExisting(path), nil
	case "file-missing":
		return !isFileExisting(path), nil
	default:
		return false, fmt.Errorf("unknown builtin predicate %q", verb)
	}
}

// EvalCmd runs the shell command in projectRoot via "sh -c <command>".
// Returns true if the command exits with code 0.
// Intentionally uses the POSIX-portable "sh" rather than config.ShellBin — conditions
// must be predictably portable regardless of the project's configured shell.
func EvalCmd(command, projectRoot string) (bool, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, fmt.Errorf("cmd condition: empty command")
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = projectRoot
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// Non-zero exit is a valid "false" result, not an error.
	var exitErr *exec.ExitError
	if ok := isExitError(err, &exitErr); ok {
		return false, nil
	}
	return false, fmt.Errorf("cmd condition %q: %w", command, err)
}

// EvalRuntime evaluates a runtime condition (builtin predicate or cmd:).
// expr must satisfy IsRuntime(expr) == true; template expressions are not handled here.
func EvalRuntime(expr, projectRoot string) (bool, error) {
	kind, payload := Classify(expr)
	switch kind {
	case KindBuiltin:
		return EvalBuiltin(payload, projectRoot)
	case KindCmd:
		return EvalCmd(payload, projectRoot)
	default:
		return false, fmt.Errorf("EvalRuntime called with non-runtime expression %q", expr)
	}
}

// --- helpers ---

func isDirExisting(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// isDirEmpty returns true when path does not exist or is an empty directory.
func isDirEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("read dir %q: %w", path, err)
	}
	return len(entries) == 0, nil
}

func isFileExisting(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// isExitError type-asserts err to *exec.ExitError, setting target if successful.
func isExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok && target != nil {
		*target = e
	}
	return ok
}

// Type is the kind of a typed condition.
type Type string

const (
	// TypeBuiltin resolves to a predicate name in the condition registry.
	// Example: {Type: "builtin", Cmd: "dir-empty foo"}.
	TypeBuiltin Type = "builtin"

	// TypeShell runs a shell command via sh -c; exit 0 = true.
	// Example: {Type: "shell", Cmd: "test -f foo"}.
	TypeShell Type = "shell"

	// TypeTemplate is a Go template expression, evaluated at plan time.
	// Example: {Type: "template", Expr: "{{ .Services.main.Enabled }}"}.
	TypeTemplate Type = "template"
)

// Condition is a typed when-condition for deploy/reset/lifecycle steps.
// Exactly one of Cmd (for builtin/shell) or Expr (for template) must be set.
type Condition struct {
	Type Type           `yaml:"type"`
	Cmd  string         `yaml:"cmd,omitempty"`
	Expr string         `yaml:"expr,omitempty"`
	With map[string]any `yaml:"with,omitempty"` // reserved; predicate args today live in Cmd
}

// UnmarshalYAML rejects string shorthand (legacy syntax) with a clear error.
// Only the mapping form {type: ..., cmd: ..., expr: ...} is accepted.
func (c *Condition) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("when: must be a mapping (e.g., {type: builtin, cmd: dir-empty foo}), not a scalar string")
	}
	known := map[string]bool{"type": true, "cmd": true, "expr": true, "with": true}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if key := node.Content[i].Value; !known[key] {
			return fmt.Errorf("when: unknown field %q", key)
		}
	}
	type conditionAlias Condition
	var ca conditionAlias
	if err := node.Decode(&ca); err != nil {
		return err
	}
	*c = Condition(ca)
	return nil
}

// Validate checks that the Condition is well-formed.
// - builtin or shell: Cmd must be non-empty
// - template: Expr must be non-empty
func (c *Condition) Validate() error {
	switch c.Type {
	case TypeBuiltin, TypeShell:
		if c.Cmd == "" {
			return fmt.Errorf("condition type %q requires non-empty cmd", c.Type)
		}
	case TypeTemplate:
		if c.Expr == "" {
			return fmt.Errorf("condition type %q requires non-empty expr", c.Type)
		}
	default:
		return fmt.Errorf("unknown condition type %q", c.Type)
	}
	return nil
}

// IsRuntime reports whether this condition requires runtime (execution-time) evaluation.
// Template conditions are evaluated at plan time; builtin and shell conditions are runtime.
func (c *Condition) IsRuntime() bool {
	return c.Type == TypeBuiltin || c.Type == TypeShell
}

// EvalRuntimeTyped evaluates a runtime-kind typed condition.
// Returns error if called with a template condition (which should be evaluated at plan time by the caller).
func EvalRuntimeTyped(c *Condition, projectRoot string) (bool, error) {
	if c == nil {
		return true, nil
	}
	switch c.Type {
	case TypeBuiltin:
		return EvalBuiltin(c.Cmd, projectRoot)
	case TypeShell:
		return EvalCmd(c.Cmd, projectRoot)
	default:
		return false, fmt.Errorf("EvalRuntimeTyped: %s is not a runtime kind", c.Type)
	}
}
