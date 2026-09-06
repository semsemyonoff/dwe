package cmdctx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

// The output-file helpers below are shared by every command that writes a
// user-named file (`dwe secrets encrypt|decrypt`, `dwe deploy|reset eject`).
// They live here rather than in one command package because an overwrite policy
// that exists twice is two policies nothing cross-checks: the refusal wording,
// the --force escape hatch, the non-regular-file guard and the path discipline
// have to be the same file-to-file or the difference only surfaces to a user.
//
// The error codes are namespaced per caller: the helpers take a code prefix and
// build "<prefix>_path_invalid", "<prefix>_output_exists",
// "<prefix>_output_invalid" and "<prefix>_output_write_failed", so a command's
// JSON envelope never carries another command's code.
const (
	codePathInvalid       = "path_invalid"
	codeOutputExists      = "output_exists"
	codeOutputInvalid     = "output_invalid"
	codeOutputWriteFailed = "output_write_failed"
)

func outputCode(prefix, suffix string) string { return prefix + "_" + suffix }

// ResolveFilePath makes a user-supplied path absolute and applies the same
// discipline the config-pack renderer applies to its own sources: a path inside
// the project must stay inside it and must not travel through a symlinked
// component, and neither end may be a symlink or a device.
//
// A path outside the project (an absolute /tmp target, say) is allowed — these
// are file utilities, not pack loaders — but still may not be a symlink: writing
// through one is how a "decrypt to a scratch file" turns into an overwrite of
// something else.
//
// role names the end of the command in the error text ("input" / "output"), and
// codePrefix namespaces the error code (see the block comment above).
func ResolveFilePath(codePrefix, projectRoot, path, role string) (string, error) {
	code := outputCode(codePrefix, codePathInvalid)
	if strings.TrimSpace(path) == "" {
		return "", Err(code, "empty "+role+" path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", ErrWrap(code, fmt.Errorf("resolve %s path %s: %w", role, path, err))
	}
	if PathIsUnder(projectRoot, abs) {
		if _, err := pathsafe.ContainedRel(projectRoot, abs); err != nil {
			return "", ErrWrap(code, fmt.Errorf("%s path %s: %w", role, path, err))
		}
		if err := pathsafe.CheckNoSymlinks(projectRoot, abs, role+" path"); err != nil {
			return "", ErrWrap(code, err)
		}
	}

	fi, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Fine for an output; a caller reading the path reports a missing input
		// by name itself.
		return abs, nil
	case err != nil:
		return "", ErrWrap(code, fmt.Errorf("stat %s: %w", abs, err))
	case fi.Mode()&os.ModeSymlink != 0:
		return "", Err(code,
			fmt.Sprintf("%s is a symlink; symlinked %s paths are not supported", abs, role)).
			WithDetail("path", abs)
	case !fi.Mode().IsRegular():
		return "", Err(code,
			fmt.Sprintf("%s is not a regular file (mode %s)", abs, fi.Mode())).
			WithDetail("path", abs)
	}
	return abs, nil
}

// OutputFile is one write on its way to disk: the resolved target (through
// ResolveFilePath), the bytes, and the policy knobs the caller decides.
type OutputFile struct {
	Path  string
	Data  []byte
	Mode  os.FileMode
	Force bool

	// TightenMode chmods an already-existing target to Mode before the write.
	// os.WriteFile keeps a pre-existing file's mode, so a caller writing
	// something sensitive (a decrypted secret) must ask for this; a caller
	// writing an ordinary source file (an ejected pipeline) must not, or it
	// would silently re-permission a file the repository already owns.
	TightenMode bool

	// ExistsNote is an optional explanation of why the existing file matters,
	// appended to the refusal message. The helper loads nothing and knows
	// nothing about the target's content — the caller supplies the sentence
	// (see InertPipelineNote).
	ExistsNote string
}

// WriteOutputFile writes the result, refusing to clobber an existing file
// without Force. The mode is applied with an explicit Chmod because WriteFile
// leaves an existing file's mode alone — which would leave a decrypted secret at
// whatever the previous file happened to be.
func WriteOutputFile(codePrefix string, f OutputFile) error {
	existed := false
	if fi, err := os.Lstat(f.Path); err == nil {
		if !f.Force {
			msg := fmt.Sprintf("%s already exists", f.Path)
			if f.ExistsNote != "" {
				msg += " — " + f.ExistsNote
			}
			return Err(outputCode(codePrefix, codeOutputExists), msg).
				WithDetail("path", f.Path).
				WithHint("pass --force to overwrite it, or choose another --out PATH")
		}
		if !fi.Mode().IsRegular() {
			return Err(outputCode(codePrefix, codeOutputInvalid),
				fmt.Sprintf("%s is not a regular file (mode %s)", f.Path, fi.Mode())).
				WithDetail("path", f.Path)
		}
		existed = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ErrWrap(outputCode(codePrefix, codeOutputWriteFailed), err)
	}

	// Only tighten when the caller asked: an overwritten ciphertext file keeps
	// whatever the repository gave it, while a decrypted plaintext file is
	// always forced down to its mode. The chmod runs BEFORE the write —
	// os.WriteFile keeps a pre-existing mode, so tightening afterwards would
	// leave the plaintext world-readable for the length of the write (and
	// permanently if the process dies in between).
	if existed && f.TightenMode {
		if err := os.Chmod(f.Path, f.Mode); err != nil {
			return ErrWrap(outputCode(codePrefix, codeOutputWriteFailed), err)
		}
	}
	if err := os.WriteFile(f.Path, f.Data, f.Mode); err != nil {
		return ErrWrap(outputCode(codePrefix, codeOutputWriteFailed), err)
	}
	return nil
}

// InertPipelineNote describes an existing pipeline file that dwe does not
// actually run, for WriteOutputFile's refusal. It reports inert on the same two
// conditions `dwe validate` uses (internal/core/validate/config/workspace.go):
// the file parsed as a default fallback (empty or all comments), or it declares
// no phases — a file carrying only `log: false` is inert to the validator and
// must be inert here too, or `eject` would call authored what `validate` calls
// inert. An authored pipeline gets no note.
func InertPipelineNote(state config.PipelineFileState, phases int, pipelineName string) string {
	switch {
	case state == config.PipelineStateDefaultFallback:
		return "it has no active content (all comments or empty), so the built-in default " + pipelineName + " pipeline is what runs today"
	case phases == 0:
		return "it declares no phases, so the built-in default " + pipelineName + " pipeline is what runs today"
	}
	return ""
}

// PathIsUnder reports whether abs lives inside root.
func PathIsUnder(root, abs string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
