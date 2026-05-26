// Package prompt renders a compact, shell-prompt-ready segment for the current
// devbox project. Optimised for per-prompt invocation: avoids cobra, lipgloss,
// and config validation. Bypassed from cmd/devbox/main.go before cobra is
// constructed.
package prompt

import (
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const configFilename = "devbox.yml"

type devboxStub struct {
	Project struct {
		Name string `yaml:"name"`
	} `yaml:"project"`
}

// Run resolves the current working directory and dispatches to runFromDir.
// Returns process exit code: 0 inside a project, 1 outside or on silent failure.
func Run(stdout io.Writer, args []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		return 1
	}
	return runFromDir(stdout, args, cwd)
}

func runFromDir(stdout io.Writer, args []string, cwd string) int {
	check, ok := parseArgs(args)
	if !ok {
		return 1
	}

	root, found := findRoot(cwd)
	if !found {
		return 1
	}

	name, ok := readProjectName(root)
	if !ok {
		return 1
	}

	if check {
		return 0
	}

	if _, err := io.WriteString(stdout, "{▪} "+name+"\n"); err != nil {
		return 1
	}
	return 0
}

// parseArgs returns (checkMode, ok). ok=false means args are malformed and the
// caller should exit silently with code 1.
func parseArgs(args []string) (check bool, ok bool) {
	switch len(args) {
	case 0:
		return false, true
	case 1:
		if args[0] == "--check" {
			return true, true
		}
	}
	return false, false
}

// findRoot walks up from start looking for devbox.yml. Returns the directory
// containing it. Does NOT resolve symlinks (intentional: prompt does not care
// about canonical paths, and skipping EvalSymlinks saves syscalls).
func findRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, configFilename)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// readProjectName returns the project name and ok=true on success. ok=false
// signals a hard read/parse failure (corrupted devbox.yml) and the caller
// should exit silently with code 1.
func readProjectName(root string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(root, configFilename))
	if err != nil {
		return "", false
	}
	var stub devboxStub
	if err := yaml.Unmarshal(data, &stub); err != nil {
		return "", false
	}
	if stub.Project.Name == "" {
		return filepath.Base(root), true
	}
	return stub.Project.Name, true
}
