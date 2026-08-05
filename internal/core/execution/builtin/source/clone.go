package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// Clone is the `source_clone` action builtin: it clones a git repository into a
// project-relative directory with the idempotency gate built in, replacing the
// `when:`/`check:` pair workspaces used to wrap a hand-rolled clone command.
//
// Behaviour by destination state:
//   - contains a `.git` entry — skip with a message (success), regardless of the
//     branch that checkout is on: source_clone materialises the source once and
//     never touches an existing working tree.
//   - absent or empty — clone.
//   - non-empty and not a git checkout — error naming the path.
type Clone struct{}

// cloneParams holds the validated `with:` parameters.
type cloneParams struct {
	repo   string
	dir    string
	branch string
}

func parseCloneParams(with map[string]any) (cloneParams, error) {
	p := cloneParams{
		repo:   spec.GetStringParam(with, "repo", ""),
		dir:    spec.GetStringParam(with, "dir", ""),
		branch: spec.GetStringParam(with, "branch", ""),
	}
	if p.repo == "" {
		return p, errors.New("missing required param 'repo'")
	}
	if p.dir == "" {
		return p, errors.New("missing required param 'dir'")
	}
	if filepath.IsAbs(p.dir) {
		return p, fmt.Errorf("'dir' %q must be relative to the project root", p.dir)
	}
	cleaned := filepath.Clean(p.dir)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return p, fmt.Errorf("'dir' %q is not allowed (root-equivalent or escaping the project root)", p.dir)
	}
	return p, nil
}

// Validate checks that repo and dir are present and that dir stays inside the
// project.
func (Clone) Validate(with map[string]any) error {
	if _, err := parseCloneParams(with); err != nil {
		return fmt.Errorf("builtin source_clone: %w", err)
	}
	return nil
}

// Describe returns a one-line summary for plan output.
func (Clone) Describe(with map[string]any) string {
	repo := spec.GetStringParam(with, "repo", "")
	dir := spec.GetStringParam(with, "dir", "")
	branch := spec.GetStringParam(with, "branch", "")
	if branch != "" {
		return fmt.Sprintf("builtin: source_clone(repo=%s, dir=%s, branch=%s)", repo, dir, branch)
	}
	return fmt.Sprintf("builtin: source_clone(repo=%s, dir=%s)", repo, dir)
}

// destState classifies the clone destination.
type destState int

const (
	destMissing destState = iota
	destEmpty
	destGit
	destNonEmpty
	destNotDir
)

func classifyDest(abs string) (destState, error) {
	fi, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return destMissing, nil
		}
		return destMissing, fmt.Errorf("stat %s: %w", abs, err)
	}
	if !fi.IsDir() {
		return destNotDir, nil
	}
	// A `.git` entry may be a directory (normal checkout) or a file (worktree,
	// submodule) — either means "already cloned".
	if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
		return destGit, nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return destMissing, fmt.Errorf("read %s: %w", abs, err)
	}
	if len(entries) == 0 {
		return destEmpty, nil
	}
	return destNonEmpty, nil
}

// Run clones the repository unless the destination is already a git checkout.
func (Clone) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	p, err := parseCloneParams(with)
	if err != nil {
		return fmt.Errorf("source_clone: %w", err)
	}
	if ectx.ProjectRoot == "" {
		return errors.New("source_clone: project root is not set")
	}

	abs := filepath.Join(ectx.ProjectRoot, p.dir)
	if _, err := pathsafe.ContainedRel(ectx.ProjectRoot, abs); err != nil {
		return fmt.Errorf("source_clone: dir %q escapes the project root: %w", p.dir, err)
	}
	if err := pathsafe.CheckNoSymlinks(ectx.ProjectRoot, abs, "clone destination"); err != nil {
		return fmt.Errorf("source_clone: %w", err)
	}

	state, err := classifyDest(abs)
	if err != nil {
		return fmt.Errorf("source_clone: %w", err)
	}
	switch state {
	case destGit:
		writeInfo(ectx, fmt.Sprintf("source already cloned: %s (skipping)", p.dir))
		return nil
	case destNotDir:
		return fmt.Errorf("source_clone: destination %q exists and is not a directory", p.dir)
	case destNonEmpty:
		return fmt.Errorf("source_clone: destination %q is not empty and is not a git checkout; remove it or clone into it manually", p.dir)
	case destMissing, destEmpty:
		// fall through to clone
	}

	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("source_clone: create parent dir for %q: %w", p.dir, err)
	}
	// Post-creation boundary re-check: MkdirAll follows symlinks, so confirm the
	// resolved parent is still inside the project root.
	realRoot, err := filepath.EvalSymlinks(ectx.ProjectRoot)
	if err != nil {
		return fmt.Errorf("source_clone: resolve project root: %w", err)
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("source_clone: resolve parent dir for %q: %w", p.dir, err)
	}
	if err := pathsafe.EnsureRealUnder(realParent, realRoot); err != nil {
		return fmt.Errorf("source_clone: destination %q resolves outside the project root via symlink: %w", p.dir, err)
	}

	gitBin := config.GitBin(ectx.Config)
	// `ext::<cmd>` is a git transport that runs <cmd> as a host program. Git
	// already refuses it by default, but a user-level `protocol.ext.allow=always`
	// re-enables it — and `repo` can come from `vars:`, which a container may be
	// allowed to write via `bridge.vars_writable`. Pin the policy on the command
	// line (highest precedence) so a repo URL is never a way to execute a host
	// command; anyone who genuinely wants to run a program writes a shell step.
	args := []string{"-c", "protocol.ext.allow=never", "clone"}
	if p.branch != "" {
		args = append(args, "--branch", p.branch)
	}
	args = append(args, "--", p.repo, abs)

	trace.Command(ctx, gitBin, args...)

	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, gitBin, args...) //nolint:gosec // binary from config accessor, args are not shell-parsed
	cmd.Dir = ectx.ProjectRoot
	// Never read the user's input: a credential prompt inside a deploy is a hang.
	cmd.Stdin = nil
	cmd.Stdout = outWriter(ectx)
	cmd.Stderr = &stderr
	cmd.Env = nonInteractiveGitEnv(os.Environ())
	// Orphan descendants (ssh) would keep the stderr pipe open past a cancel.
	cmd.WaitDelay = 100 * time.Millisecond

	if err := cmd.Run(); err != nil {
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			return fmt.Errorf("source_clone: git clone %s: %w: %s", p.repo, err, tail)
		}
		return fmt.Errorf("source_clone: git clone %s: %w", p.repo, err)
	}
	writeSuccess(ectx, fmt.Sprintf("cloned %s into %s", p.repo, p.dir))
	return nil
}

// nonInteractiveGitEnv forces git into a fail-fast posture: every clone in a
// pipeline targets a private host, and a credential or host-key prompt there is
// an unattended hang rather than a question anyone answers. GIT_ASKPASS and
// SSH_ASKPASS are set *empty* on purpose — git then falls through to the
// terminal prompt, which GIT_TERMINAL_PROMPT=0 turns into an immediate error.
// GIT_SSH_COMMAND is only defaulted, never overridden: an author who set it to
// an actual command (custom identity file, port) means it. An EMPTY value counts
// as unset — unlike the askpass pair above, where empty is the meaningful state
// that disables the helper, an empty ssh command is not a way to say "use the
// default ssh": git would take it literally and the clone could not run at all.
// Nothing can be expressed by clearing it, so inherited empties get the default.
func nonInteractiveGitEnv(base []string) []string {
	// os/exec keeps the last occurrence of each key, so appending overrides.
	env := append(append([]string{}, base...),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
	)
	if !hasNonEmptyEnv(base, "GIT_SSH_COMMAND") {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}

// hasNonEmptyEnv reports whether key is present in env with a non-empty value.
// A later entry wins, matching os/exec's last-occurrence rule.
func hasNonEmptyEnv(env []string, key string) bool {
	prefix := key + "="
	found := false
	for _, kv := range env {
		if val, ok := strings.CutPrefix(kv, prefix); ok {
			found = val != ""
		}
	}
	return found
}

func outWriter(ectx spec.ExecContext) io.Writer {
	if ectx.Output == nil {
		return io.Discard
	}
	return ectx.Output.Writer()
}

func writeInfo(ectx spec.ExecContext, msg string) {
	if ectx.Output != nil {
		ectx.Output.Info(msg)
	}
}

func writeSuccess(ectx spec.ExecContext, msg string) {
	if ectx.Output != nil {
		ectx.Output.Success(msg)
	}
}
