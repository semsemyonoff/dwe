//go:build windows

package bridge

import "errors"

// The bridge daemon does not run on Windows hosts (design D2 — host-side
// Windows dwe is out of scope; WSL2-distro dwe is the Linux case). These
// stubs only keep the package compiling for cross-builds.

func spawnDetached(_ SpawnSpec) error {
	return errors.New("bridge: the bridge daemon is not supported on Windows hosts")
}

func terminateDaemonOS(_ int) error {
	return errors.New("bridge: the bridge daemon is not supported on Windows hosts")
}
