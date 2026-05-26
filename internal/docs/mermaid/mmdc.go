package mermaid

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const mmdcTimeout = 10 * time.Second

// MmdcRenderer renders mermaid diagrams using the mmdc CLI.
type MmdcRenderer struct {
	Bin     string
	Version func() string
	Strict  bool // if true, ErrNotFound becomes ErrMmdcRequired
}

// NewMmdc constructs an MmdcRenderer with a given binary name.
// It probes the version once at construction time (via sync.OnceValue in callers).
// If strict is true, missing mmdc returns ErrMmdcRequired instead of ErrMmdcNotAvailable.
func NewMmdc(bin string, strict bool) *MmdcRenderer {
	return &MmdcRenderer{
		Bin:    bin,
		Strict: strict,
		Version: func() string {
			return probeMmdcVersion(bin)
		},
	}
}

// Render invokes mmdc to render mermaid source to PNG.
func (m *MmdcRenderer) Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error) {
	// Create temp files for input and output.
	tmpDir := os.TempDir()
	inputFile := filepath.Join(tmpDir, "mermaid_input.mmd")
	outputFile := filepath.Join(tmpDir, "mermaid_output.png")

	// Write input mermaid source.
	if err := os.WriteFile(inputFile, []byte(src), 0o600); err != nil {
		return nil, fmt.Errorf("write mermaid input: %w", err)
	}
	defer os.Remove(inputFile)
	defer os.Remove(outputFile)

	// Invoke mmdc with timeout.
	ctx, cancel := context.WithTimeout(ctx, mmdcTimeout)
	defer cancel()

	themeArg := "light"
	if theme == ThemeDark {
		themeArg = "dark"
	}

	cmd := exec.CommandContext(ctx, m.Bin, "-i", inputFile, "-o", outputFile,
		"-b", "transparent", "-t", themeArg, "--width", strconv.Itoa(width), "--quiet")

	// Platform-specific setup (see mmdc_unix.go / mmdc_windows.go).
	configureCommand(cmd)

	// Run the command.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check for "not found" before checking other errors.
		// On Unix, this is os.ErrNotExist; exec.ErrNotFound may not match.
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
			if m.Strict {
				return nil, ErrMmdcRequired
			}
			return nil, ErrMmdcNotAvailable
		}

		// Timeout or other execution error.
		if ctx.Err() == context.DeadlineExceeded {
			return nil, context.DeadlineExceeded
		}

		// Return the full error message.
		return nil, fmt.Errorf("mmdc render failed: %w (stderr: %s)", err, stderr.String())
	}

	// Read the output PNG.
	png, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("read mermaid output: %w", err)
	}

	return png, nil
}

// probeMmdcVersion runs mmdc --version and returns the version string or "unknown" on error.
func probeMmdcVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "--version")
	configureCommand(cmd)

	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}

	return strings.TrimSpace(string(out))
}

// New constructs a mermaid renderer chain with cache, mmdc, and fallback.
// It wraps mmdc with a file cache (XDG-aware).
// If strict is true, missing mmdc returns ErrMmdcRequired; otherwise ErrMmdcNotAvailable.
func New(bin string, cacheDir string, capBytes int64, strict bool) Renderer {
	versionOnce := sync.OnceValue(func() string {
		return probeMmdcVersion(bin)
	})

	mmdc := NewMmdc(bin, strict)
	mmdc.Version = versionOnce

	cache := NewFileCache(cacheDir, capBytes, mmdc, versionOnce)
	return cache
}
