package keygate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// Marker and file states reported by the inventory. They are part of the
// `dwe secrets status` JSON contract, so they are stable strings.
const (
	StateDecrypted      = "decrypted"
	StateUnresolved     = "unresolved"
	StateDecryptable    = "decryptable"
	StateNotDecryptable = "not decryptable"
)

// ReasonStaleKey qualifies a readable row that only a STRAGGLER keyfile opened
// — the configured recipient's identity does not.
//
// It is what makes the half-rekeyed report actionable. The config loader tries
// the configured identity alone, so such a value is `wrong_identity` at load
// time and `secrets.unresolved` blocks the lifecycle commands; without this
// qualifier `status` printed the row green and empty, i.e. "nothing to do", in
// exactly the recovery scenario `rekey`'s resume hint sends the user here for.
const ReasonStaleKey = "stale_key"

// ReasonUnreadable is the token for an .age source the scan could not read at
// all — a path-discipline refusal (symlink, non-regular file) or an I/O error.
//
// The cause travels in FileRow.Detail, never in Reason: `reason` is a closed
// vocabulary a consumer switches on, and putting an OS message (which carries
// an absolute path) there made exactly the rows a report must classify
// unclassifiable.
const ReasonUnreadable = "unreadable"

// configPackKind is the only template-pack kind that may carry .age sources:
// ide/ai/git pack outputs are git-tracked and render against a sanitized
// config, so an encrypted source there would have nowhere safe to land.
const configPackKind = "config"

// IdentitySet is every identity this machine can offer for a project: the one
// configured recipient's, plus every other keyfile in the keys directory.
//
// The stragglers exist for rekey recovery (decision 11): an interrupted rekey
// leaves markers under two recipients, and the inventory must be able to say
// which of them each value belongs to instead of reporting the whole tree as
// broken.
type IdentitySet struct {
	recipient string
	primary   secrets.Identity
	source    secrets.Source
	err       error
	others    []secrets.Identity
}

// LoadIdentitySet resolves the configured identity and the fallbacks. Neither
// lookup failing is an error: the inventory reports per value what could be
// read, and a keyless machine is a normal, documented state.
//
// The configured recipient is excluded from the stragglers ONLY when the
// primary lookup succeeded. LoadIdentity is first-present-source-wins with no
// fall-through and only ever reads `keys/<recipient>.key`, so when it fails the
// keys directory can still hold that very identity — under a foreign name, or
// canonically while a `DWE_AGE_KEY` exported for another project shadowed it.
// Excluding it unconditionally dropped the one key that opens the tree, which
// made every value read `no_identity` and let `init --replace-recipient`'s
// readability guard orphan a project whose key sat on disk.
func LoadIdentitySet(recipient string) IdentitySet {
	set := IdentitySet{recipient: recipient}
	if recipient == "" {
		set.err = fmt.Errorf("%w: no secrets.recipient configured", secrets.ErrNoIdentity)
		return set
	}
	set.primary, set.source, set.err = secrets.LoadIdentity(recipient)
	exclude := recipient
	if set.err != nil {
		exclude = ""
	}
	others, err := secrets.LoadAnyIdentity(exclude)
	if err == nil {
		set.others = others
	}
	return set
}

// Recipient is the configured secrets.recipient this set was loaded for.
func (s IdentitySet) Recipient() string { return s.recipient }

// Source names where the configured identity came from, or SourceNone.
func (s IdentitySet) Source() secrets.Source { return s.source }

// Err is the configured identity's load failure, nil when it loaded.
func (s IdentitySet) Err() error { return s.err }

// Reason maps the configured identity's load failure onto the stable
// SecretsState reason strings, so status, the validators and the loader all
// name the same causes.
func (s IdentitySet) Reason() string { return IdentityReason(s.err) }

// IdentityReason maps an identity-LOAD failure onto the stable
// config.Reason* strings. It is the CLI-side mirror of config.identityReason
// (packages.md pins that the two must agree) and the single implementation
// behind IdentitySet.Reason and Result.IdentityReason.
func IdentityReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, secrets.ErrWrongIdentity):
		return config.ReasonWrongIdentity
	case errors.Is(err, secrets.ErrInvalidIdentity):
		return config.ReasonInvalidIdentity
	default:
		return config.ReasonNoIdentity
	}
}

// classifyMarker reports whether a marker can be opened on this machine.
// A damaged payload is detected without any key at all (CheckMarker), so a
// keyless developer is never sent hunting for a key that would not have helped.
func (s IdentitySet) classifyMarker(marker string) (state, reason string) {
	if err := secrets.CheckMarker(marker); err != nil {
		return StateUnresolved, config.ReasonCorrupt
	}
	var primaryErr error
	if s.err == nil {
		_, primaryErr = secrets.Decrypt(marker, s.primary)
		if primaryErr == nil {
			return StateDecrypted, ""
		}
	}
	for _, id := range s.others {
		if _, err := secrets.Decrypt(marker, id); err == nil {
			return StateDecrypted, ReasonStaleKey
		}
	}
	if s.err == nil {
		// The configured identity loaded but does not open this value. CheckMarker
		// only proves the header and the base64: a body truncated in a bad merge
		// still fails as ErrCorrupt here, and reporting that as wrong_identity
		// would contradict the loader, which calls the same value corrupt.
		if errors.Is(primaryErr, secrets.ErrCorrupt) {
			return StateUnresolved, config.ReasonCorrupt
		}
		// Encrypted to a different recipient (a half-finished rekey, a bad merge).
		return StateUnresolved, config.ReasonWrongIdentity
	}
	return StateUnresolved, s.Reason()
}

// Decrypt opens a marker with whatever this machine holds: the configured
// identity first, then the stragglers (a half-rekeyed tree). A damaged payload
// is reported as such without a key, so the failure names the real cause
// instead of blaming a missing identity.
func (s IdentitySet) Decrypt(marker string) (string, error) {
	if err := secrets.CheckMarker(marker); err != nil {
		return "", err
	}
	var primaryErr error
	if s.err == nil {
		var plain string
		plain, primaryErr = secrets.Decrypt(marker, s.primary)
		if primaryErr == nil {
			return plain, nil
		}
	}
	for _, id := range s.others {
		if plain, err := secrets.Decrypt(marker, id); err == nil {
			return plain, nil
		}
	}
	if s.err != nil {
		return "", s.err
	}
	// CheckMarker cleared the header and the base64, so a failure here is either
	// a body no key can open or the wrong recipient; blaming the recipient for a
	// damaged body would send a rekey after a value no rekey can read.
	if errors.Is(primaryErr, secrets.ErrCorrupt) {
		return "", primaryErr
	}
	return "", fmt.Errorf("%w: this value is encrypted to another recipient than %s", secrets.ErrWrongIdentity, s.recipient)
}

// DecryptBytes is Decrypt for a native age file: the configured identity first,
// then the stragglers a half-rekeyed tree leaves behind.
func (s IdentitySet) DecryptBytes(data []byte) ([]byte, error) {
	var primaryErr error
	if s.err == nil {
		var plain []byte
		plain, primaryErr = secrets.DecryptBytes(data, s.primary)
		if primaryErr == nil {
			return plain, nil
		}
	}
	for _, id := range s.others {
		if plain, err := secrets.DecryptBytes(data, id); err == nil {
			return plain, nil
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	// Same rule as Decrypt: a damaged file must not be reported as the wrong
	// recipient, or the user is sent to rekey a file no key can open instead of
	// restoring it from git — which is what `dwe validate secrets` tells them.
	if errors.Is(primaryErr, secrets.ErrCorrupt) {
		return nil, primaryErr
	}
	return nil, fmt.Errorf("%w: this file is encrypted to another recipient than %s", secrets.ErrWrongIdentity, s.recipient)
}

// classifyBytes is classifyMarker for a native age file — including the
// identity-free damage check first, for the same reason: a truncated pack
// source is detectable without any key, and reporting it as `no_identity` on a
// keyless machine sends the reader after a key that would not have opened it.
func (s IdentitySet) classifyBytes(data []byte) (state, reason string) {
	if err := secrets.CheckAgeFile(data); err != nil {
		return StateNotDecryptable, config.ReasonCorrupt
	}
	var primaryErr error
	if s.err == nil {
		if _, primaryErr = secrets.DecryptBytes(data, s.primary); primaryErr == nil {
			return StateDecryptable, ""
		}
	}
	for _, id := range s.others {
		if _, err := secrets.DecryptBytes(data, id); err == nil {
			return StateDecryptable, ReasonStaleKey
		}
	}
	if s.err == nil {
		// A truncated pack source fails as ErrCorrupt here; calling that the
		// wrong recipient would contradict `dwe validate secrets`, which reads
		// the same file through the same decoder.
		if errors.Is(primaryErr, secrets.ErrCorrupt) {
			return StateNotDecryptable, config.ReasonCorrupt
		}
		return StateNotDecryptable, config.ReasonWrongIdentity
	}
	return StateNotDecryptable, s.Reason()
}

// MarkerRow is one ENC[age:…] scalar in the raw layers, with where it lives and
// whether this machine can read it. The json tags ARE the `dwe secrets status`
// payload contract.
type MarkerRow struct {
	Layer  string `json:"layer"` // layer file, relative to the project root
	Path   string `json:"path"`  // dot-path inside that layer
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// FileRow is one *.age file under workspace/templates/config/**.
type FileRow struct {
	File   string `json:"file"` // relative to the project root
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Detail is the free-form cause behind ReasonUnreadable (an OS message or a
	// path-discipline refusal). It is display text, never a token to switch on.
	Detail string `json:"detail,omitempty"`
}

// Result is the whole encrypted surface of a project: the identity this machine
// holds, every committed marker, and every encrypted pack source.
// `dwe secrets status` renders it; `rekey` walks the same lists.
type Result struct {
	Recipient      string
	IdentitySource secrets.Source
	IdentityErr    error
	Markers        []MarkerRow
	Files          []FileRow
}

// HasSecrets reports whether the project carries anything encrypted at all.
func (r Result) HasSecrets() bool { return len(r.Markers) > 0 || len(r.Files) > 0 }

// IdentityReason names why the configured identity did not load, in the stable
// config.Reason* vocabulary; "" when it loaded.
func (r Result) IdentityReason() string { return IdentityReason(r.IdentityErr) }

// Readable counts the rows this machine can actually open: the two numbers the
// post-import report turns into "N encrypted value(s) and M .age file(s) are
// now readable". A stale-key row is NOT counted — the configured identity does
// not open it, so it is still a to-do.
func (r Result) Readable() (markers, files int) {
	for _, m := range r.Markers {
		if m.State == StateDecrypted && m.Reason == "" {
			markers++
		}
	}
	for _, f := range r.Files {
		if f.State == StateDecryptable && f.Reason == "" {
			files++
		}
	}
	return markers, files
}

// Inventory builds the encrypted surface from the raw layers plus a filesystem
// scan of the config-pack templates. Rows are sorted (layer order then path for
// markers, path order for files) so every table and JSON dump built from it is
// byte-stable across runs.
func Inventory(baseDir string, layers []config.Layer, ids IdentitySet) (Result, error) {
	res := Result{Recipient: ids.recipient, IdentitySource: ids.source, IdentityErr: ids.err}

	// CollectMarkers is the single marker inventory (layer order, then path), so
	// status can never disagree with the loader about sequence indices or key
	// order.
	for _, m := range config.CollectMarkers(layers) {
		state, reason := ids.classifyMarker(m.Value)
		res.Markers = append(res.Markers, MarkerRow{
			Layer:  RelToRoot(baseDir, m.Layer),
			Path:   m.Path,
			State:  state,
			Reason: reason,
		})
	}

	files, err := CollectAgeFiles(baseDir)
	if err != nil {
		return Result{}, err
	}
	for _, f := range files {
		row := FileRow{File: RelToRoot(baseDir, f.Path), State: StateNotDecryptable}
		switch {
		case f.Err != nil:
			row.Reason, row.Detail = ReasonUnreadable, f.Err.Error()
		default:
			data, rerr := os.ReadFile(f.Path)
			if rerr != nil {
				row.Reason, row.Detail = ReasonUnreadable, rerr.Error()
				break
			}
			row.State, row.Reason = ids.classifyBytes(data)
		}
		res.Files = append(res.Files, row)
	}
	return res, nil
}

// AgeFile is one discovered *.age source; Err carries the path-discipline
// refusal for a candidate that exists but must not be read.
type AgeFile struct {
	Path string
	Err  error
}

// CollectAgeFiles finds every *.age file under workspace/templates/config,
// including the *.local override packs. Each candidate goes through the same
// containment + symlink discipline packroot applies at render time
// (ContainedRel, CheckNoSymlinks, regular-file Lstat), and a candidate that
// fails it is REPORTED rather than skipped: a symlinked "secret" is exactly the
// thing a status report must not stay silent about.
func CollectAgeFiles(projectRoot string) ([]AgeFile, error) {
	if projectRoot == "" {
		return nil, nil
	}
	root := ageRoot(projectRoot)
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}

	var out []AgeFile
	// WalkDir does not follow symlinks, so a symlinked directory is reported as
	// an entry (and rejected below) rather than silently traversed.
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".age") {
			return nil
		}
		out = append(out, inspectAgeFile(root, path))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// HasEncryptedSurface is the cheap probe behind the onboarding gate: does this
// project carry anything an identity would be needed for? No decryption and no
// identity lookup, so a healthy `dwe run` never pays for a per-invocation
// decrypt scan. A walk error counts as "no surface": the gate must never
// introduce a failure the caller would not otherwise have hit.
func HasEncryptedSurface(baseDir string, layers []config.Layer) bool {
	if len(config.CollectMarkers(layers)) > 0 {
		return true
	}
	return hasAgeFile(baseDir)
}

// hasAgeFile stops at the first *.age candidate; unlike CollectAgeFiles it
// applies no path discipline, because "is there a surface at all" is answered
// by the entry's existence, not by whether it may be read.
func hasAgeFile(projectRoot string) bool {
	if projectRoot == "" {
		return false
	}
	found := false
	err := filepath.WalkDir(ageRoot(projectRoot), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".age") {
			return nil
		}
		found = true
		return fs.SkipAll
	})
	if err != nil {
		return false
	}
	return found
}

// ageRoot is the one directory both scans walk.
func ageRoot(projectRoot string) string {
	return filepath.Join(projectRoot, "workspace", "templates", configPackKind)
}

// inspectAgeFile applies the pack path discipline to one candidate.
func inspectAgeFile(root, path string) AgeFile {
	const label = "config templates"
	if _, err := pathsafe.ContainedRel(root, path); err != nil {
		return AgeFile{Path: path, Err: err}
	}
	if err := pathsafe.CheckNoSymlinks(root, path, label); err != nil {
		return AgeFile{Path: path, Err: err}
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return AgeFile{Path: path, Err: err}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return AgeFile{Path: path, Err: fmt.Errorf("%s is a symlink; symlinked template sources are not supported", path)}
	}
	if !fi.Mode().IsRegular() {
		return AgeFile{Path: path, Err: fmt.Errorf("%s is not a regular file (mode %s)", path, fi.Mode())}
	}
	return AgeFile{Path: path}
}

// RelToRoot renders a path relative to the project root for display; an
// unrelatable path is shown as-is.
func RelToRoot(projectRoot, path string) string {
	if projectRoot == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path
	}
	return rel
}
