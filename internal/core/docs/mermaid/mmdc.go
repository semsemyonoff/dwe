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
	"sync/atomic"
	"time"
)

const mmdcTimeout = 10 * time.Second

// renderOptsVersion is mixed into the cache key so cached PNGs that were
// produced under a different mmdc invocation (different `--scale`, theme
// arg shape, etc.) are not silently reused after this code changes.
// Bump whenever the flags below change in a way that affects the output
// bytes.
const renderOptsVersion = "v2-scale3"

// MmdcRenderer renders mermaid diagrams using the mmdc CLI.
type MmdcRenderer struct {
	Bin     string
	Version func() string
	Strict  bool // if true, ErrNotFound becomes ErrMmdcRequired
}

// NewMmdc constructs an MmdcRenderer with a given binary name.
// Version is a lazy probe closure; New replaces it with a non-blocking one.
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
	// Use a unique temp dir per invocation to avoid races when multiple goroutines render concurrently.
	tmpDir, err := os.MkdirTemp("", "mermaid-*")
	if err != nil {
		return nil, fmt.Errorf("create mermaid temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	inputFile := filepath.Join(tmpDir, "input.mmd")
	outputFile := filepath.Join(tmpDir, "output.png")

	// Write input mermaid source.
	if err := os.WriteFile(inputFile, []byte(src), 0o600); err != nil {
		return nil, fmt.Errorf("write mermaid input: %w", err)
	}

	// Invoke mmdc with timeout.
	ctx, cancel := context.WithTimeout(ctx, mmdcTimeout)
	defer cancel()

	themeArg := "light"
	if theme == ThemeDark {
		themeArg = "dark"
	}

	// --scale 3 turns on Puppeteer's HiDPI rendering so the PNG is sampled
	// at 3× the logical width. The output PNG ends up width*3 pixels wide,
	// which keeps text and edges crisp in the system viewer (and on retina
	// displays) instead of looking pixelated at the cost of ~3× file size.
	cmd := exec.CommandContext(ctx, m.Bin, "-i", inputFile, "-o", outputFile,
		"-b", "transparent", "-t", themeArg,
		"--width", strconv.Itoa(width),
		"--scale", "3",
		"--quiet")

	// Platform-specific setup (see mmdc_unix.go).
	configureCommand(cmd)

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

		// Timeout: kill the entire process group to reap Chrome/Puppeteer children.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			killCommandGroup(cmd)
			return nil, context.DeadlineExceeded
		}

		return nil, fmt.Errorf("mmdc render failed: %w (stderr: %s)", err, stderr.String())
	}

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
// It wraps mmdc with a file cache (XDG-aware). If strict is true, missing
// mmdc returns ErrMmdcRequired; otherwise ErrMmdcNotAvailable.
//
// The version accessor returned to the cache is truly non-blocking:
//   - A background goroutine runs probeMmdcVersion exactly once and stores
//     the result in an atomic.Value.
//   - Callers (cache lookup / cache key) read the atomic. Before the probe
//     completes they see "unknown"; afterwards they see the real version.
//
// We intentionally avoid sync.OnceValue here: although it memoises after
// the first call, concurrent callers BLOCK on the in-flight invocation —
// so a UI-thread cacheKey hitting the OnceValue before the pre-warmed
// goroutine returns still waits the up-to-2s probe timeout. The atomic
// pattern below cannot block under any condition.
//
// Cache-key stability: "unknown" is a deterministic value, so cache keys
// computed before the probe completes are stable and reproducible across
// runs — they just don't pin to a specific mmdc version. After the probe
// completes, subsequent keys include the real version (effectively
// invalidating any "unknown"-bucketed entries, which were rare because
// the probe normally returns in <100ms).
func New(bin string, cacheDir string, capBytes int64, strict bool) Renderer {
	versionFn := nonblockingVersion(bin)

	mmdc := NewMmdc(bin, strict)
	mmdc.Version = versionFn

	return NewFileCache(cacheDir, capBytes, mmdc, versionFn)
}

// nonblockingVersion returns a closure that reports the current cached
// mmdc version without ever blocking. A background goroutine populates the
// real value asynchronously; until it lands, the closure returns "unknown".
func nonblockingVersion(bin string) func() string {
	var v atomic.Value
	v.Store("unknown")
	go func() {
		v.Store(probeMmdcVersion(bin))
	}()
	return func() string {
		s, _ := v.Load().(string)
		return s
	}
}
