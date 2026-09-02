// Package secrets implements the `dwe secrets` command tree: the project's age
// key pair, the ENC[age:…] markers committed in the config layers, and the
// native *.age sources of config packs.
//
// The tree is deliberately host-only: `secrets` is absent from
// bridgeAllowedTopLevel, so no container can mint, rekey or export an identity.
// Reads of already-decrypted values stay reachable through `dwe vars get`,
// which is the exposure a container already has through its rendered .env.
//
// Writers take the project locks (no preflight — this is not a stack
// mutation); status / get / key export are read-only. Every command routes its
// payload through cmdctx.WriteData and returns typed cmdctx errors, so
// --output json stays parseable.
package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// stdoutIsTerminal is the production tty probe behind the `key export` warning;
// tests swap it. Package-level state: tests overriding it MUST NOT run in
// parallel.
var stdoutIsTerminal = func() bool { return term.IsTerminal(os.Stdout.Fd()) }

// Marker and file states reported by the inventory. They are part of the
// `dwe secrets status` JSON contract, so they are stable strings.
const (
	stateDecrypted      = "decrypted"
	stateUnresolved     = "unresolved"
	stateDecryptable    = "decryptable"
	stateNotDecryptable = "not decryptable"
)

// reasonStaleKey qualifies a readable row that only a STRAGGLER keyfile opened
// — the configured recipient's identity does not.
//
// It is what makes the half-rekeyed report actionable. The config loader tries
// the configured identity alone, so such a value is `wrong_identity` at load
// time and `secrets.unresolved` blocks the lifecycle commands; without this
// qualifier `status` printed the row green and empty, i.e. "nothing to do", in
// exactly the recovery scenario `rekey`'s resume hint sends the user here for.
const reasonStaleKey = "stale_key"

// configPackKind is the only template-pack kind that may carry .age sources:
// ide/ai/git pack outputs are git-tracked and render against a sanitized
// config, so an encrypted source there would have nowhere safe to land.
const configPackKind = "config"

// NewCmd builds the `dwe secrets` command tree. Registered under
// groupConfiguration in cli/root.go next to `vars`: both edit the same three
// layer files, and a secret is just a var that happens to be encrypted.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secrets",
		Aliases: []string{"secret"},
		GroupID: groupID,
		Short:   "Manage the project's encrypted secrets",
		Long: `Work with encrypted-at-rest secrets committed to the repository.

One X25519 age key pair per project: the public recipient is committed as
secrets.recipient in workspace.yml, so anyone with the repository can ADD a
secret; the private identity lives in ` + "`~/" + secrets.KeysDirRel + "/<recipient>.key`" + ` (or in
` + secrets.EnvKey + ` / ` + secrets.EnvKeyFile + ` for CI), so only identity holders can read one.

Secrets take two shapes: an ENC[age:…] scalar in any config layer (decrypted in
memory at load time, so ${vars.*} and exports.env see plaintext), and a whole
*.age file used as a config-pack source.

Without a usable identity the project still loads — markers stay literal,
` + "`dwe vars`" + ` shows <encrypted>, and the lifecycle commands stop with a named
fix instead of writing ciphertext into a config file.`,
		Example: `  dwe secrets status
  dwe secrets init
  dwe secrets set vars.telegram.token
  dwe secrets get vars.telegram.token
  dwe secrets key export
  dwe secrets key import --file identity.txt`,
		SilenceUsage: true,
	}

	cmd.AddCommand(newInitCmd(flags))
	cmd.AddCommand(newStatusCmd(flags))
	cmd.AddCommand(newSetCmd(flags))
	cmd.AddCommand(newGetCmd(flags))
	cmd.AddCommand(newKeyCmd(flags))
	cmd.AddCommand(newEncryptCmd(flags))
	cmd.AddCommand(newDecryptCmd(flags))
	cmd.AddCommand(newRekeyCmd(flags))
	return cmd
}

// loadRawLayers reads the config layers as written (ciphertext) and validates
// their roots. Every `secrets` command works on the raw tree: the whole point
// is to see the markers, which a decrypting load would have replaced.
func loadRawLayers(flags *cmdctx.RootFlags) ([]config.Layer, error) {
	layers, err := config.LoadRawLayers(flags.ConfigPath)
	if err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	// The same validation LoadConfig runs, so `secrets` refuses to write into a
	// tree that would not load afterwards (a secrets: block in the wrong layer,
	// a malformed recipient).
	if err := config.ValidateLayerRoots(layers); err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	return layers, nil
}

// requireRecipient returns the configured secrets.recipient, or a typed error
// naming `dwe secrets init` when the project has none.
func requireRecipient(flags *cmdctx.RootFlags) (string, error) {
	layers, err := loadRawLayers(flags)
	if err != nil {
		return "", err
	}
	return recipientOrErr(layers)
}

// identitySet is every identity this machine can offer for a project: the one
// configured recipient's, plus every other keyfile in the keys directory.
//
// The stragglers exist for rekey recovery (decision 11): an interrupted rekey
// leaves markers under two recipients, and the inventory must be able to say
// which of them each value belongs to instead of reporting the whole tree as
// broken.
type identitySet struct {
	recipient string
	primary   secrets.Identity
	source    secrets.Source
	err       error
	others    []secrets.Identity
}

// loadIdentitySet resolves the configured identity and the fallbacks. Neither
// lookup failing is an error: the inventory reports per value what could be
// read, and a keyless machine is a normal, documented state.
func loadIdentitySet(recipient string) identitySet {
	set := identitySet{recipient: recipient}
	if recipient == "" {
		set.err = fmt.Errorf("%w: no secrets.recipient configured", secrets.ErrNoIdentity)
		return set
	}
	set.primary, set.source, set.err = secrets.LoadIdentity(recipient)
	others, err := secrets.LoadAnyIdentity(recipient)
	if err == nil {
		set.others = others
	}
	return set
}

// reason maps the configured identity's load failure onto the stable
// SecretsState reason strings, so status, the validators and the loader all
// name the same causes.
func (s identitySet) reason() string {
	switch {
	case s.err == nil:
		return ""
	case errors.Is(s.err, secrets.ErrWrongIdentity):
		return config.ReasonWrongIdentity
	default:
		return config.ReasonNoIdentity
	}
}

// classifyMarker reports whether a marker can be opened on this machine.
// A damaged payload is detected without any key at all (CheckMarker), so a
// keyless developer is never sent hunting for a key that would not have helped.
func (s identitySet) classifyMarker(marker string) (state, reason string) {
	if err := secrets.CheckMarker(marker); err != nil {
		return stateUnresolved, config.ReasonCorrupt
	}
	if s.err == nil {
		if _, err := secrets.Decrypt(marker, s.primary); err == nil {
			return stateDecrypted, ""
		}
	}
	for _, id := range s.others {
		if _, err := secrets.Decrypt(marker, id); err == nil {
			return stateDecrypted, reasonStaleKey
		}
	}
	if s.err == nil {
		// The configured identity loaded but does not open this value: it was
		// encrypted to a different recipient (a half-finished rekey, a bad merge).
		return stateUnresolved, config.ReasonWrongIdentity
	}
	return stateUnresolved, s.reason()
}

// decrypt opens a marker with whatever this machine holds: the configured
// identity first, then the stragglers (a half-rekeyed tree). A damaged payload
// is reported as such without a key, so the failure names the real cause
// instead of blaming a missing identity.
func (s identitySet) decrypt(marker string) (string, error) {
	if err := secrets.CheckMarker(marker); err != nil {
		return "", err
	}
	if s.err == nil {
		if plain, err := secrets.Decrypt(marker, s.primary); err == nil {
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
	return "", fmt.Errorf("%w: this value is encrypted to another recipient than %s", secrets.ErrWrongIdentity, s.recipient)
}

// decryptBytes is decrypt for a native age file: the configured identity first,
// then the stragglers a half-rekeyed tree leaves behind.
func (s identitySet) decryptBytes(data []byte) ([]byte, error) {
	if s.err == nil {
		if plain, err := secrets.DecryptBytes(data, s.primary); err == nil {
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
	return nil, fmt.Errorf("%w: this file is encrypted to another recipient than %s", secrets.ErrWrongIdentity, s.recipient)
}

// classifyBytes is classifyMarker for a native age file.
func (s identitySet) classifyBytes(data []byte) (state, reason string) {
	if s.err == nil {
		if _, err := secrets.DecryptBytes(data, s.primary); err == nil {
			return stateDecryptable, ""
		}
	}
	for _, id := range s.others {
		if _, err := secrets.DecryptBytes(data, id); err == nil {
			return stateDecryptable, reasonStaleKey
		}
	}
	if s.err == nil {
		return stateNotDecryptable, config.ReasonWrongIdentity
	}
	return stateNotDecryptable, s.reason()
}

// markerRow is one ENC[age:…] scalar in the raw layers, with where it lives and
// whether this machine can read it.
type markerRow struct {
	Layer  string `json:"layer"` // layer file, relative to the project root
	Path   string `json:"path"`  // dot-path inside that layer
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// fileRow is one *.age file under workspace/templates/config/**.
type fileRow struct {
	File   string `json:"file"` // relative to the project root
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// inventory is the whole encrypted surface of a project: the identity this
// machine holds, every committed marker, and every encrypted pack source.
// `dwe secrets status` renders it; `rekey` walks the same lists.
type inventory struct {
	Recipient      string
	IdentitySource secrets.Source
	IdentityErr    error
	Markers        []markerRow
	Files          []fileRow
}

// HasSecrets reports whether the project carries anything encrypted at all.
func (inv inventory) HasSecrets() bool { return len(inv.Markers) > 0 || len(inv.Files) > 0 }

// collectInventory builds the inventory from the raw layers plus a filesystem
// scan of the config-pack templates. Rows are sorted (layer order then path for
// markers, path order for files) so every table and JSON dump built from it is
// byte-stable across runs.
func collectInventory(flags *cmdctx.RootFlags) (inventory, error) {
	layers, err := loadRawLayers(flags)
	if err != nil {
		return inventory{}, err
	}
	root := flags.ProjectRoot()
	recipient := config.RecipientFromLayers(layers)
	ids := loadIdentitySet(recipient)

	inv := inventory{Recipient: recipient, IdentitySource: ids.source, IdentityErr: ids.err}

	// CollectMarkers is the single marker inventory (layer order, then path), so
	// status can never disagree with the loader about sequence indices or key
	// order.
	for _, m := range config.CollectMarkers(layers) {
		state, reason := ids.classifyMarker(m.Value)
		inv.Markers = append(inv.Markers, markerRow{
			Layer:  relToRoot(root, m.Layer),
			Path:   m.Path,
			State:  state,
			Reason: reason,
		})
	}

	files, err := collectAgeFiles(root)
	if err != nil {
		return inventory{}, err
	}
	for _, f := range files {
		row := fileRow{File: relToRoot(root, f.path), State: stateNotDecryptable}
		switch {
		case f.err != nil:
			row.Reason = f.err.Error()
		default:
			data, rerr := os.ReadFile(f.path)
			if rerr != nil {
				row.Reason = rerr.Error()
				break
			}
			row.State, row.Reason = ids.classifyBytes(data)
		}
		inv.Files = append(inv.Files, row)
	}
	return inv, nil
}

// ageFile is one discovered *.age source; err carries the path-discipline
// refusal for a candidate that exists but must not be read.
type ageFile struct {
	path string
	err  error
}

// collectAgeFiles finds every *.age file under workspace/templates/config,
// including the *.local override packs. Each candidate goes through the same
// containment + symlink discipline packroot applies at render time
// (ContainedRel, CheckNoSymlinks, regular-file Lstat), and a candidate that
// fails it is REPORTED rather than skipped: a symlinked "secret" is exactly the
// thing a status report must not stay silent about.
func collectAgeFiles(projectRoot string) ([]ageFile, error) {
	if projectRoot == "" {
		return nil, nil
	}
	root := filepath.Join(projectRoot, "workspace", "templates", configPackKind)
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}

	var out []ageFile
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
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// inspectAgeFile applies the pack path discipline to one candidate.
func inspectAgeFile(root, path string) ageFile {
	const label = "config templates"
	if _, err := pathsafe.ContainedRel(root, path); err != nil {
		return ageFile{path: path, err: err}
	}
	if err := pathsafe.CheckNoSymlinks(root, path, label); err != nil {
		return ageFile{path: path, err: err}
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return ageFile{path: path, err: err}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ageFile{path: path, err: fmt.Errorf("%s is a symlink; symlinked template sources are not supported", path)}
	}
	if !fi.Mode().IsRegular() {
		return ageFile{path: path, err: fmt.Errorf("%s is not a regular file (mode %s)", path, fi.Mode())}
	}
	return ageFile{path: path}
}

// relToRoot renders a path relative to the project root for display; an
// unrelatable path is shown as-is.
func relToRoot(projectRoot, path string) string {
	if projectRoot == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path
	}
	return rel
}

// displayKeyfilePath renders the keyfile location for a recipient, degrading to
// the generic form when the home directory or the recipient is unusable (the
// message is help text, never a hard failure).
func displayKeyfilePath(recipient string) string {
	if path, err := secrets.KeyfilePath(recipient); err == nil {
		return path
	}
	return "~/" + secrets.KeysDirRel + string(os.PathSeparator) + "<recipient>.key"
}
