package linters

import (
	"bytes"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// genericMessageCap bounds the combined stdout+stderr embedded in a generic
// adapter's diagnostic message. The 50 MB stream cap in runtime.go protects
// memory; this cap protects the rendered diagnostics table from a single
// failing linter blowing up the layout.
const genericMessageCap = 2 * 1024

// GenericAdapter is the catch-all adapter used when an entry in validate.yml
// declares type: generic. It runs `bin <flags> -- <files...>` and, on
// non-zero exit, emits one error-severity diagnostic with combined output as
// the message. No per-line parsing — the user owns the entire flag surface
// and the output is opaque to us.
type GenericAdapter struct {
	id  string
	bin string
}

// NewGeneric constructs a generic adapter bound to the given id and default
// binary name.
func NewGeneric(id, defaultBin string) *GenericAdapter {
	return &GenericAdapter{id: id, bin: defaultBin}
}

// ID returns the adapter identifier.
func (g *GenericAdapter) ID() string { return g.id }

// DefaultBin returns the bare command name to invoke.
func (g *GenericAdapter) DefaultBin() string { return g.bin }

// DefaultPaths returns nil — generic adapters have no default paths; the user
// must declare them explicitly in validate.yml.
func (g *GenericAdapter) DefaultPaths() []string { return nil }

// DefaultExtensions returns nil — generic adapters have no default extensions.
func (g *GenericAdapter) DefaultExtensions() []string { return nil }

// DefaultFilenames returns nil — generic adapters have no default filenames.
func (g *GenericAdapter) DefaultFilenames() []string { return nil }

// ReservedFlags returns nil — generic adapters don't parse output, so the
// user owns the entire flag surface.
func (g *GenericAdapter) ReservedFlags() []string { return nil }

// BuildArgs returns the argv (excluding bin): user flags, then "--", then
// files. The "--" separator stops filenames starting with "-" from being
// treated as flags by the underlying linter.
func (g *GenericAdapter) BuildArgs(files []string, userFlags []string) []string {
	args := make([]string, 0, len(userFlags)+1+len(files))
	args = append(args, userFlags...)
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	return args
}

// ParseOutput converts the subprocess result into diagnostics. Exit 0 → no
// diagnostics (clean). Non-zero → one error-severity diagnostic with combined
// stdout+stderr as the message, truncated to genericMessageCap bytes.
func (g *GenericAdapter) ParseOutput(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error) {
	if exitCode == 0 {
		return nil, nil
	}
	var combined bytes.Buffer
	if len(stdout) > 0 {
		combined.Write(stdout)
	}
	if len(stderr) > 0 {
		if combined.Len() > 0 {
			combined.WriteByte('\n')
		}
		combined.Write(stderr)
	}
	msg := strings.TrimRight(combined.String(), "\n")
	if msg == "" {
		msg = "linter exited non-zero with no output"
	}
	if len(msg) > genericMessageCap {
		// strings.ToValidUTF8 ensures the byte-boundary truncation never leaves
		// a partial multi-byte rune in the message.
		msg = strings.ToValidUTF8(msg[:genericMessageCap], "") + "\n…(truncated)"
	}
	return []validate.Diagnostic{
		finding(g.id, validate.SeverityError, "", 0, msg, ""),
	}, nil
}
