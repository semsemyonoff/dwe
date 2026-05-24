package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// maxInspectManifestBytes caps the in-archive manifest payload an inspector
// will read. The manifest is plain YAML — 1 MiB is generous and protects
// `devbox snapshot inspect <tar>` from a malicious tar that points to an
// absurdly large "manifest.yml" entry.
const maxInspectManifestBytes = 1 << 20

// ReadManifestFromTar opens a .tar.gz archive at tarPath and decodes the
// embedded manifest.yml without extracting any other entry.
//
// Safety: the function rejects archive entry types that could be used to
// escape (symlink / hardlink / device / fifo), caps the manifest payload
// at maxInspectManifestBytes, and treats the *first* entry whose basename
// is "manifest.yml" as authoritative. Returns an error when no manifest
// entry is present.
func ReadManifestFromTar(tarPath string) (*Manifest, error) {
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

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: tar: %w", tarPath, err)
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return nil, fmt.Errorf("read %s: rejected disallowed tar entry type for %q", tarPath, hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Name != ManifestFileName {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxInspectManifestBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", tarPath, err)
		}
		if int64(len(data)) > maxInspectManifestBytes {
			return nil, fmt.Errorf("read %s: manifest entry exceeds %d bytes", tarPath, maxInspectManifestBytes)
		}
		var m Manifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse %s manifest: %w", tarPath, err)
		}
		return &m, nil
	}
	return nil, fmt.Errorf("read %s: no manifest.yml found in archive", tarPath)
}
