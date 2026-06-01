// Package snapshot implements the dwe snapshot subsystem: workflow
// orchestration (create, restore, remove, list, exec) and project-level
// scanning helpers (workspace files, services diff). Workflow execution is
// driven by exec.go.
//
// Descriptor types and coordinates (Manifest, paths, name validation,
// artifact scan, current-pointer state) live in the meta/ subpackage;
// tar I/O (pack, unpack, verify, inspect) lives in archive/.
package snapshot
