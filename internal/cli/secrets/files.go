package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// ageExt marks a native age file. A config-pack source whose from: ends in it is
// decrypted by the renderer before the usual ${...} pass.
const ageExt = ".age"

// stdoutTarget is the --out value that streams raw bytes to stdout instead of
// writing a file.
const stdoutTarget = "-"

// codePrefix namespaces the error codes the shared output-file helpers build:
// secrets_path_invalid, secrets_output_exists, secrets_output_invalid and
// secrets_output_write_failed, exactly as they read before the helpers moved
// into cmdctx.
const codePrefix = "secrets"

// The two ends of a file command, named in path-discipline errors.
const (
	roleInput  = "input"
	roleOutput = "output"
)

// Output modes. Ciphertext is meant to be committed, so it keeps the ordinary
// tracked-file mode; plaintext coming back out of an .age file is a secret on
// disk and is tightened to 0600 whether or not the target already existed.
const (
	ciphertextMode = os.FileMode(0o644)
	plaintextMode  = os.FileMode(0o600)
)

// secretFileJSON is the `encrypt` / `decrypt` payload: which file was read and
// where the result landed. Paths inside the project are relative to its root,
// like every other path in this tree; a path outside it stays absolute.
type secretFileJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func newEncryptCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		out   string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "encrypt <file>",
		Short: "Encrypt a whole file to the project's recipient",
		Long: `Encrypt a file into a native age file, for use as a config-pack source.

The output is written next to the input as <file>` + ageExt + ` unless --out says
otherwise. Point a config pack's from: at it and dwe decrypts it before the
usual ${...} render:

  render:
    - from: google-credentials.json` + ageExt + `
      to: config/google-credentials.json

The to: path is never derived from the source, so name the output whatever the
service expects. Encryption needs only the committed recipient, so anyone with
the repository can add an encrypted file; reading one back needs the identity.`,
		Example: `  dwe secrets encrypt workspace/templates/config/bot/creds.json
  dwe secrets encrypt creds.json --out workspace/templates/config/bot/creds.json` + ageExt + `
  dwe secrets encrypt creds.json --out -`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEncrypt(cmd, flags, args[0], out, force)
		},
	}
	addFileFlags(cmd, &out, &force)
	return cmd
}

func newDecryptCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		out   string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "decrypt <file" + ageExt + ">",
		Short: "Decrypt a whole age file",
		Long: `Decrypt a native age file back to plaintext.

The output path defaults to the input with its ` + ageExt + ` suffix removed; an input
that does not end in ` + ageExt + ` needs an explicit --out. A written output file is
0600 (an existing one is tightened), because it is now plaintext on disk — do
not commit it.

Use --out - to stream the plaintext to stdout instead.`,
		Example: `  dwe secrets decrypt workspace/templates/config/bot/creds.json` + ageExt + `
  dwe secrets decrypt creds.json` + ageExt + ` --out -`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecrypt(cmd, flags, args[0], out, force)
		},
	}
	addFileFlags(cmd, &out, &force)
	return cmd
}

// addFileFlags registers the two flags both file commands share.
//
// The flag is --out, not -o: the root command already owns -o for --output, and
// a second shorthand would either panic at wiring time or shadow the format
// flag. The same reason `dwe docs llms-txt` spells it --out.
func addFileFlags(cmd *cobra.Command, out *string, force *bool) {
	cmd.Flags().StringVar(out, "out", "", "write to PATH ('"+stdoutTarget+"' streams to stdout) instead of the default name")
	cmd.Flags().BoolVar(force, "force", false, "overwrite an existing output file")
}

func runEncrypt(cmd *cobra.Command, flags *cmdctx.RootFlags, input, out string, force bool) error {
	if err := checkStreamMode(flags, out); err != nil {
		return err
	}
	root := flags.ProjectRoot()
	src, err := resolveFilePath(root, input, roleInput)
	if err != nil {
		return err
	}
	plain, err := readInputFile(src)
	if err != nil {
		return err
	}

	// The locks cover the recipient read as well as the write: a concurrent
	// `rekey` between the two would otherwise leave a file encrypted to the
	// retired recipient while workspace.yml already advertises the new one.
	w := sharedrender.NewWriter(cmd.ErrOrStderr())
	release, err := cmdctx.AcquireProjectLocksOrReport(root, w)
	if err != nil {
		return err
	}
	defer release()

	recipient, err := requireRecipient(flags)
	if err != nil {
		return err
	}

	data, err := secrets.EncryptBytes(plain, recipient)
	if err != nil {
		return cmdctx.ErrWrap("secrets_encrypt_failed", err)
	}
	return emitFile(cmd, flags, fileWrite{
		root:   root,
		src:    src,
		out:    out,
		def:    src + ageExt,
		data:   data,
		mode:   ciphertextMode,
		force:  force,
		render: func(d secretFileJSON) string { return render.SecretsFileResult("encrypted", d.From, d.To, "") },
	})
}

func runDecrypt(cmd *cobra.Command, flags *cmdctx.RootFlags, input, out string, force bool) error {
	if err := checkStreamMode(flags, out); err != nil {
		return err
	}
	recipient, err := requireRecipient(flags)
	if err != nil {
		return err
	}

	root := flags.ProjectRoot()
	src, err := resolveFilePath(root, input, roleInput)
	if err != nil {
		return err
	}
	ciphertext, err := readInputFile(src)
	if err != nil {
		return err
	}

	// Read-only as far as the project is concerned: no locks. The identity set
	// opens a file left behind by an interrupted rekey, exactly as `get` does
	// for a scalar.
	plain, err := keygate.LoadIdentitySet(recipient).DecryptBytes(ciphertext)
	if err != nil {
		return fileDecryptError(recipient, displayPath(root, src), err)
	}

	def := strings.TrimSuffix(src, ageExt)
	if def == src {
		def = ""
	}
	return emitFile(cmd, flags, fileWrite{
		root:  root,
		src:   src,
		out:   out,
		def:   def,
		data:  plain,
		mode:  plaintextMode,
		force: force,
		render: func(d secretFileJSON) string {
			return render.SecretsFileResult("decrypted", d.From, d.To, "(mode 0600)")
		},
	})
}

// fileWrite is one resolved encrypt/decrypt result on its way to disk or stdout.
// def is the default output path, empty when the command cannot derive one.
type fileWrite struct {
	root   string
	src    string
	out    string
	def    string
	data   []byte
	mode   os.FileMode
	force  bool
	render func(secretFileJSON) string
}

// emitFile writes the result and reports it, or streams it to stdout for
// --out -. The stdout path deliberately prints nothing else: the bytes are the
// whole output, so a status line would corrupt a redirect.
func emitFile(cmd *cobra.Command, flags *cmdctx.RootFlags, fw fileWrite) error {
	if fw.out == stdoutTarget {
		if _, err := cmd.OutOrStdout().Write(fw.data); err != nil {
			return cmdctx.ErrWrap("secrets_output_write_failed", err)
		}
		return nil
	}

	target := fw.out
	if target == "" {
		if fw.def == "" {
			return cmdctx.Err("secrets_output_required",
				fmt.Sprintf("cannot derive an output name from %s", displayPath(fw.root, fw.src))).
				WithDetail("from", displayPath(fw.root, fw.src)).
				WithHint("pass --out PATH (the input does not end in " + ageExt + ")")
		}
		target = fw.def
	}
	dst, err := resolveFilePath(fw.root, target, roleOutput)
	if err != nil {
		return err
	}
	if dst == fw.src {
		return cmdctx.Err("secrets_output_invalid", "the output path is the input path").
			WithDetail("path", displayPath(fw.root, dst))
	}
	if err := writeOutputFile(dst, fw.data, fw.mode, fw.force); err != nil {
		return err
	}

	data := secretFileJSON{From: displayPath(fw.root, fw.src), To: displayPath(fw.root, dst)}
	return cmdctx.WriteData(flags, cmd, data, fw.render)
}

// checkStreamMode refuses `--out -` together with `--output json`: the stream is
// raw bytes and there is no way to also put a JSON envelope on the same stdout
// without corrupting one of the two.
func checkStreamMode(flags *cmdctx.RootFlags, out string) error {
	if out != stdoutTarget || flags.Output != "json" {
		return nil
	}
	return cmdctx.Err("secrets_raw_stream",
		"--out "+stdoutTarget+" streams raw bytes to stdout and cannot be combined with --output json").
		WithHint("drop --output json, or write the result to a file")
}

// resolveFilePath applies the shared path discipline under the secrets code
// prefix, so every message and code stays what it was before the helper moved
// into cmdctx.
func resolveFilePath(projectRoot, path, role string) (string, error) {
	return cmdctx.ResolveFilePath(codePrefix, projectRoot, path, role)
}

// readInputFile reads a required input, naming a missing file rather than
// letting the generic read error carry the whole explanation.
func readInputFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, cmdctx.Err("secrets_input_missing", fmt.Sprintf("%s does not exist", path)).
				WithDetail("path", path)
		}
		return nil, cmdctx.ErrWrap("secrets_input_read_failed", err)
	}
	return data, nil
}

// writeOutputFile writes the result through the shared cmdctx helper under the
// secrets code prefix. Only the plaintext mode is tightened on an existing
// file: an overwritten ciphertext file keeps whatever the repository gave it,
// while a decrypted plaintext file is always forced down to 0600.
func writeOutputFile(path string, data []byte, mode os.FileMode, force bool) error {
	return cmdctx.WriteOutputFile(codePrefix, cmdctx.OutputFile{
		Path:        path,
		Data:        data,
		Mode:        mode,
		Force:       force,
		TightenMode: mode == plaintextMode,
	})
}

// fileDecryptError names the file that could not be opened, routing a missing
// identity through the shared "where the lookup looked" hint.
func fileDecryptError(recipient, file string, err error) error {
	if errors.Is(err, secrets.ErrNoIdentity) {
		return identityError(recipient, err).WithDetail("file", file)
	}
	return cmdctx.ErrWrap("secrets_decrypt_failed", err).
		WithDetail("file", file).
		WithHint("run 'dwe secrets status' to see the state of every encrypted file")
}

// displayPath renders a path relative to the project root when it lives inside
// it, and absolute otherwise — a "../../tmp/x" would be worse than the real path.
func displayPath(root, abs string) string {
	if cmdctx.PathIsUnder(root, abs) {
		return relToRoot(root, abs)
	}
	return abs
}
