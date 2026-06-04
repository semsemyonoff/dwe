// Package hostid resolves the UID/GID to use inside containers.
//
// On macOS, Docker Desktop runs containers in a Linux VM where host UIDs
// (e.g. 501) don't exist in the container's /etc/passwd. The convention is to
// use 1000:1000, matching the UID/GID baked into the image at build time. On
// Linux, the actual host UID/GID is returned so file permissions match.
//
// This leaf package is the single source of truth for that platform ladder,
// shared by the .env renderer (envfile) and the command template engine (tpl).
package hostid

import (
	"os/user"
	"runtime"
)

// Info holds the resolved host UID/GID.
type Info struct {
	UID string
	GID string
}

// Current returns the host UID/GID using the platform ladder: 1000:1000 on
// macOS (or when the current user can't be resolved), the real host IDs on
// Linux. It reads user.Current() once so UID and GID always come from the same
// snapshot.
func Current() Info {
	info := Info{UID: "1000", GID: "1000"}
	if runtime.GOOS == "darwin" {
		return info
	}
	u, err := user.Current()
	if err != nil {
		return info
	}
	info.UID = u.Uid
	info.GID = u.Gid
	return info
}

// UID returns the host UID (see Current for the platform ladder).
func UID() string { return Current().UID }

// GID returns the host GID (see Current for the platform ladder).
func GID() string { return Current().GID }
