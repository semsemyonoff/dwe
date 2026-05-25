package linters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"devbox-cli/internal/validate"
)

// HadolintID is the stable adapter identifier.
const HadolintID = "hadolint"

// HadolintAdapter implements Adapter for hadolint. The output format is locked
// to JSON because ParseOutput depends on its shape; -f / --format is reserved
// from the user.
type HadolintAdapter struct{}

// NewHadolint returns a HadolintAdapter.
func NewHadolint() *HadolintAdapter { return &HadolintAdapter{} }

// ID returns "hadolint".
func (*HadolintAdapter) ID() string { return HadolintID }

// DefaultBin returns "hadolint".
func (*HadolintAdapter) DefaultBin() string { return "hadolint" }

// DefaultPaths returns the project root so hadolint discovers Dockerfiles
// anywhere under it.
func (*HadolintAdapter) DefaultPaths() []string { return []string{"."} }

// DefaultExtensions matches files like `service.dockerfile` alongside bare
// `Dockerfile` names handled by DefaultFilenames.
func (*HadolintAdapter) DefaultExtensions() []string { return []string{".dockerfile"} }

// DefaultFilenames returns the literal filenames hadolint inspects.
func (*HadolintAdapter) DefaultFilenames() []string { return []string{"Dockerfile"} }

// ReservedFlags forbids users from overriding the output format; the parser
// requires JSON.
func (*HadolintAdapter) ReservedFlags() []string { return []string{"-f", "--format"} }

// BuildArgs assembles `-f json <userFlags...> -- <files...>`. The forced format
// flags go first for clarity; the actual safety contract is enforced at
// adapter-binding time via ReservedFlags.
func (*HadolintAdapter) BuildArgs(files []string, userFlags []string) []string {
	args := make([]string, 0, 2+len(userFlags)+1+len(files))
	args = append(args, "-f", "json")
	args = append(args, userFlags...)
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	return args
}

// hadolintFinding mirrors a single entry from hadolint's -f json array. Only
// fields the runtime uses are decoded.
type hadolintFinding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ParseOutput decodes hadolint's JSON array. Hadolint exits non-zero whenever
// it has findings — that is normal, not a failure. A non-zero exit combined
// with empty/invalid JSON is a hadolint-internal failure and gets one error
// diagnostic with stderr as the message.
func (a *HadolintAdapter) ParseOutput(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		if exitCode == 0 {
			return nil, nil
		}
		return []validate.Diagnostic{
			finding(a.ID(), validate.SeverityError, "", 0, hadolintInternalMessage(stderr), ""),
		}, nil
	}

	var items []hadolintFinding
	if err := json.Unmarshal(trimmed, &items); err != nil {
		if exitCode != 0 {
			return []validate.Diagnostic{
				finding(a.ID(), validate.SeverityError, "", 0, hadolintInternalMessage(stderr), ""),
			}, nil
		}
		return nil, fmt.Errorf("hadolint: decode JSON: %w", err)
	}

	out := make([]validate.Diagnostic, 0, len(items))
	for _, it := range items {
		out = append(out, finding(
			a.ID(),
			hadolintSeverity(it.Level),
			it.File,
			it.Line,
			fmt.Sprintf("%s (%s)", it.Message, it.Code),
			"",
		))
	}
	return out, nil
}

func hadolintSeverity(level string) validate.Severity {
	switch strings.ToLower(level) {
	case "error":
		return validate.SeverityError
	case "warning":
		return validate.SeverityWarning
	case "info", "style":
		return validate.SeverityInfo
	default:
		return validate.SeverityWarning
	}
}

func hadolintInternalMessage(stderr []byte) string {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = "hadolint exited non-zero with no parsable output"
	}
	return msg
}
