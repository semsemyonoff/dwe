// Package snapshot implements the devbox snapshot subsystem: canonical paths,
// manifest read/write, current-pointer state, name validation, artifact scan,
// and pack/unpack support. Workflow execution is layered on top via exec.go.
//
// Descriptor types and coordinates live in the meta/ subpackage; tar I/O
// (pack/unpack/verify/inspect) lives in archive/.
package snapshot
