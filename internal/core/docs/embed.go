package docs

import (
	"embed"
	"io/fs"
	"log/slog"
)

//go:generate ../../../scripts/sync-embedded-docs.sh
//go:embed embedded
var embedFS embed.FS

// BuiltinFS is the embedded documentation file system from the binary.
var BuiltinFS fs.FS

func init() {
	// Strip the "embedded/" prefix so callers see reference/, internals/, i18n/ at the root.
	subFS, err := fs.Sub(embedFS, "embedded")
	if err != nil {
		slog.Warn("failed to create docs FS", "err", err)
		BuiltinFS = &emptyFS{}
		return
	}
	BuiltinFS = subFS

	// Sanity check: warn if the embed is effectively empty.
	entries, err := fs.ReadDir(BuiltinFS, ".")
	if err != nil {
		slog.Warn("failed to check embedded docs", "err", err)
		return
	}
	if len(entries) == 0 {
		slog.Debug("embedded docs are empty — run 'make embedded-docs' (or 'make build') to populate")
	}
}

// emptyFS implements fs.FS and returns ErrNotExist for all operations.
type emptyFS struct{}

func (e *emptyFS) Open(name string) (fs.File, error) {
	return nil, fs.ErrNotExist
}
