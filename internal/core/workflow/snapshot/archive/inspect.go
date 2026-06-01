package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
)

const (
	// maxInspectManifestBytes caps the in-archive manifest payload an inspector
	// will read. The manifest is plain YAML — 1 MiB is generous and protects
	// `dwe snapshot inspect <tar>` from a malicious tar that points to an
	// absurdly large "manifest.yml" entry.
	maxInspectManifestBytes = 1 << 20
	// maxInspectEntries caps the number of tar entries scanned before
	// manifest.yml is found. Matches the unpack entry cap.
	maxInspectEntries = 100_000
)

// ReadManifestFromTar opens a .tar.gz archive at tarPath and decodes the
// embedded manifest.yml without extracting any other entry.
//
// Safety: the function rejects archive entry types that could be used to
// escape (symlink / hardlink / device / fifo), caps the manifest payload
// at maxInspectManifestBytes, and treats the first entry with the exact
// path "manifest.yml" as authoritative. Returns an error when no manifest
// entry is present.
func ReadManifestFromTar(tarPath string) (*meta.Manifest, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: gzip: %w", tarPath, err)
	}
	defer func() { _ = gz.Close() }()

	// Cap total bytes decompressed while scanning for manifest.yml. Without
	// this, tr.Next() must decompress every unread entry body to advance past
	// it; a crafted archive with huge entries before manifest.yml would force
	// unbounded CPU/time. io.LimitReader wraps the decompressor so all reads
	// — including internal discard reads inside tr.Next() — count toward the
	// limit. Matches the unpack decompression cap.
	tr := tar.NewReader(io.LimitReader(gz, maxUnpackBytes))
	var entryCount int
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: tar: %w", tarPath, err)
		}
		entryCount++
		if entryCount > maxInspectEntries {
			return nil, fmt.Errorf("read %s: exceeded %d-entry scan limit without finding manifest.yml", tarPath, maxInspectEntries)
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return nil, fmt.Errorf("read %s: rejected disallowed tar entry type for %q", tarPath, hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Name != meta.ManifestFileName {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxInspectManifestBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", tarPath, err)
		}
		if int64(len(data)) > maxInspectManifestBytes {
			return nil, fmt.Errorf("read %s: manifest entry exceeds %d bytes", tarPath, maxInspectManifestBytes)
		}
		var m meta.Manifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse %s manifest: %w", tarPath, err)
		}
		return &m, nil
	}
	return nil, fmt.Errorf("read %s: no manifest.yml found in archive", tarPath)
}
