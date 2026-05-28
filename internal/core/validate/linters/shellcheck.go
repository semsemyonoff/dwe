package linters

import (
	"bytes"
	"encoding/json"
	"fmt"

	"devbox-cli/internal/core/validate"
)

// ShellcheckID is the stable adapter identifier.
const ShellcheckID = "shellcheck"

// ShellcheckAdapter implements Adapter for shellcheck. The output format is
// locked to JSON because ParseOutput depends on its shape; --format / -f is
// reserved from the user.
type ShellcheckAdapter struct{}

// NewShellcheck returns a ShellcheckAdapter.
func NewShellcheck() *ShellcheckAdapter { return &ShellcheckAdapter{} }

// ID returns "shellcheck".
func (*ShellcheckAdapter) ID() string { return ShellcheckID }

// DefaultBin returns "shellcheck".
func (*ShellcheckAdapter) DefaultBin() string { return "shellcheck" }

// DefaultPaths returns the conventional script directories devbox projects use.
func (*ShellcheckAdapter) DefaultPaths() []string { return []string{"devbox/scripts", "scripts"} }

// DefaultExtensions returns shell-script extensions matched by the walker.
func (*ShellcheckAdapter) DefaultExtensions() []string { return []string{".sh", ".bash"} }

// DefaultFilenames returns nil — shellcheck matches only by extension.
func (*ShellcheckAdapter) DefaultFilenames() []string { return nil }

// ReservedFlags forbids users from overriding the output format; the parser
// requires JSON.
func (*ShellcheckAdapter) ReservedFlags() []string { return []string{"--format", "-f"} }

// BuildArgs assembles `--format=json <userFlags...> -- <files...>`. The forced
// format flag goes first for clarity; the actual safety contract is enforced at
// adapter-binding time via ReservedFlags.
func (*ShellcheckAdapter) BuildArgs(files []string, userFlags []string) []string {
	args := make([]string, 0, 1+len(userFlags)+1+len(files))
	args = append(args, "--format=json")
	args = append(args, userFlags...)
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	return args
}

// shellcheckComment mirrors a single entry from shellcheck's --format=json
// array. Only fields the runtime uses are decoded.
type shellcheckComment struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Level   string `json:"level"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ParseOutput decodes shellcheck's JSON array. Shellcheck exits non-zero
// whenever it has findings — that is normal, not a failure. A non-zero exit
// combined with empty/invalid JSON is a shellcheck-internal failure and gets
// one error diagnostic with stderr as the message.
func (a *ShellcheckAdapter) ParseOutput(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		if exitCode == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("%s", nonzeroExitMessage(a.ID(), stderr))
	}

	var comments []shellcheckComment
	if err := json.Unmarshal(trimmed, &comments); err != nil {
		if exitCode != 0 {
			return nil, fmt.Errorf("%s", nonzeroExitMessage(a.ID(), stderr))
		}
		return nil, fmt.Errorf("shellcheck: decode JSON: %w", err)
	}

	out := make([]validate.Diagnostic, 0, len(comments))
	for _, c := range comments {
		out = append(out, finding(
			a.ID(),
			severityFromLevel(c.Level),
			c.File,
			c.Line,
			fmt.Sprintf("%s (SC%d)", c.Message, c.Code),
			fmt.Sprintf("https://www.shellcheck.net/wiki/SC%d", c.Code),
		))
	}
	return out, nil
}
