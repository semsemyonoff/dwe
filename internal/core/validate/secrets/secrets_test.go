package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// mustKeygen mints a throwaway project identity.
func mustKeygen(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	require.NoError(t, err)
	return id
}

// useIdentity publishes id through DWE_AGE_KEY, so no test ever reads
// ~/.config/dwe/keys.
func useIdentity(t *testing.T, id secrets.Identity) {
	t.Helper()
	t.Setenv(secrets.EnvKey, id.Export())
	t.Setenv(secrets.EnvKeyFile, "")
}

// noIdentity removes every identity source, including the keyfile directory.
func noIdentity(t *testing.T) {
	t.Helper()
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv("HOME", t.TempDir())
}

func mustEncrypt(t *testing.T, plain, recipient string) string {
	t.Helper()
	marker, err := secrets.Encrypt(plain, recipient)
	require.NoError(t, err)
	return marker
}

// writeProject writes workspace.yml and returns its path.
func writeProject(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "workspace.yml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// writeAgePack writes a config pack whose single render entry is an .age
// source, plus the service.yml-free ServiceConfig the context carries.
func writeAgePack(t *testing.T, root, packName string, ciphertext []byte) {
	t.Helper()
	packDir := filepath.Join(root, "workspace", "templates", "config", packName)
	require.NoError(t, os.MkdirAll(packDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(packDir, "manifest.yml"),
		[]byte("render:\n  - from: creds.json.age\n    to: src/creds.json\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(packDir, "creds.json.age"), ciphertext, 0o644))
}

func appServices() map[string]config.ServiceConfig {
	return map[string]config.ServiceConfig{
		"main": {Type: config.ServiceTypeApp, Enabled: true, Dir: "services/main"},
	}
}

// loadCtx builds a validate.Context the way runValidate does: a real load, with
// a nil Cfg when the load failed.
func loadCtx(t *testing.T, root, configPath string) validate.Context {
	t.Helper()
	ctx := validate.Context{ProjectRoot: root, ConfigPath: configPath}
	cfg, err := config.LoadConfig(configPath)
	if err == nil {
		ctx.Cfg = cfg
	}
	return ctx
}

func messages(diags []validate.Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.Message)
		b.WriteString(" | ")
		b.WriteString(d.Hint)
		b.WriteString("\n")
	}
	return b.String()
}

func TestRecipientValidator_silentWithoutSecrets(t *testing.T) {
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nvars:\n  plain: value\n")

	diags := (&recipientValidator{}).Run(loadCtx(t, root, path))
	require.Empty(t, diags, "a project with no secrets must produce no diagnostics")
}

func TestRecipientValidator_markersWithoutRecipient(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	noIdentity(t)
	marker := mustEncrypt(t, "s3cr3t", id.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nvars:\n  token: "+marker+"\n")

	diags := (&recipientValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityError, diags[0].Severity)
	require.Equal(t, "secrets", diags[0].Domain)
	require.Equal(t, "workspace.yml", diags[0].File)
	require.Contains(t, diags[0].Message, "secrets.recipient is not set")
	require.Contains(t, diags[0].Message, "vars.token")
	require.Contains(t, diags[0].Hint, "dwe secrets init")
}

func TestRecipientValidator_ageSourceWithoutRecipient(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	ciphertext, err := secrets.EncryptBytes([]byte("creds"), id.Recipient())
	require.NoError(t, err)
	writeAgePack(t, root, "default", ciphertext)
	path := writeProject(t, root, "project:\n  name: test\n")

	ctx := loadCtx(t, root, path)
	ctx.Cfg.Services = appServices()

	diags := (&recipientValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "encrypted config-pack source")
	require.Contains(t, diags[0].Message, "creds.json.age")
}

// A malformed recipient makes LoadConfig fail, so the scoped run arrives with a
// nil Cfg — the validator must raw-load and still diagnose it.
func TestRecipientValidator_malformedRecipientWithNilCfg(t *testing.T) {
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: age1-not-a-real-key\n")

	ctx := loadCtx(t, root, path)
	require.Nil(t, ctx.Cfg, "a malformed recipient must make LoadConfig fail")

	diags := (&recipientValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "not a valid age recipient")
	require.Contains(t, diags[0].Message, "age1-not-a-real-key")
	require.Equal(t, "workspace.yml", diags[0].File)
}

func TestRecipientValidator_corruptMarker(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	noIdentity(t)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		// Valid marker shape, valid base64, but the payload is not an age file.
		"\nvars:\n  token: ENC[age:bm90LWFuLWFnZS1maWxl]\n")

	diags := (&recipientValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1, "a damaged payload is diagnosable without any key: %s", messages(diags))
	require.Contains(t, diags[0].Message, "vars.token")
	require.Contains(t, diags[0].Message, "damaged")
	require.Equal(t, "secrets.marker:vars.token", diags[0].Target)
}

func TestRecipientValidator_validSetupIsSilent(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	marker := mustEncrypt(t, "s3cr3t", id.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+marker+"\n")

	diags := (&recipientValidator{}).Run(loadCtx(t, root, path))
	require.Empty(t, diags, messages(diags))
}

func TestUnresolvedValidator_nilCfgIsSilent(t *testing.T) {
	diags := (&unresolvedValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags, "readiness is a statement about a loaded config")
}

func TestUnresolvedValidator_noIdentity(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	noIdentity(t)
	marker := mustEncrypt(t, "s3cr3t", id.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+marker+"\n  other: "+mustEncrypt(t, "second", id.Recipient())+"\n")

	ctx := loadCtx(t, root, path)
	require.NotNil(t, ctx.Cfg, "an unresolved marker must not break the load")

	diags := (&unresolvedValidator{}).Run(ctx)
	// One row per reason, not per marker.
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, validate.SeverityError, diags[0].Severity)
	require.Equal(t, "secrets.unresolved:no_identity", diags[0].Target)
	require.Contains(t, diags[0].Message, "2 encrypted value(s)")
	require.Contains(t, diags[0].Message, "vars.other, vars.token")
	require.Contains(t, diags[0].Hint, "dwe secrets key import")
	require.Contains(t, diags[0].Hint, secrets.EnvKey)
	require.NotContains(t, messages(diags), "s3cr3t")
	require.NotContains(t, messages(diags), "AGE-SECRET-KEY-")
}

func TestUnresolvedValidator_wrongIdentity(t *testing.T) {
	root := t.TempDir()
	project := mustKeygen(t)
	stranger := mustKeygen(t)
	useIdentity(t, stranger)
	marker := mustEncrypt(t, "s3cr3t", project.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+project.Recipient()+
		"\nvars:\n  token: "+marker+"\n")

	diags := (&unresolvedValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, "secrets.unresolved:wrong_identity", diags[0].Target)
	require.Contains(t, diags[0].Message, project.Recipient())
}

func TestUnresolvedValidator_decryptedIsSilent(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	marker := mustEncrypt(t, "s3cr3t", id.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+marker+"\n")

	ctx := loadCtx(t, root, path)
	require.Equal(t, "s3cr3t", ctx.Cfg.Vars["token"])
	require.Empty(t, (&unresolvedValidator{}).Run(ctx))
}

// A file encrypted to another recipient loads fine and decrypts never: the
// failure must surface at validate time, not mid-deploy.
func TestUnresolvedValidator_ageSourceForeignRecipient(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	stranger := mustKeygen(t)
	useIdentity(t, id)
	ciphertext, err := secrets.EncryptBytes([]byte("creds"), stranger.Recipient())
	require.NoError(t, err)
	writeAgePack(t, root, "default", ciphertext)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\n")

	ctx := loadCtx(t, root, path)
	ctx.Cfg.Services = appServices()

	diags := (&unresolvedValidator{}).Run(ctx)
	require.Len(t, diags, 1, messages(diags))
	require.Contains(t, diags[0].Message, "creds.json.age")
	require.Contains(t, diags[0].Message, "cannot be decrypted")
	require.Contains(t, diags[0].Hint, "dwe secrets rekey")
}

func TestUnresolvedValidator_ageSourceTruncated(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	ciphertext, err := secrets.EncryptBytes([]byte("creds"), id.Recipient())
	require.NoError(t, err)
	writeAgePack(t, root, "default", ciphertext[:len(ciphertext)/2])
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\n")

	ctx := loadCtx(t, root, path)
	ctx.Cfg.Services = appServices()

	diags := (&unresolvedValidator{}).Run(ctx)
	require.Len(t, diags, 1, messages(diags))
	require.Contains(t, diags[0].Message, "creds.json.age")
}

func TestUnresolvedValidator_ageSourceDecryptableIsSilent(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	ciphertext, err := secrets.EncryptBytes([]byte("creds"), id.Recipient())
	require.NoError(t, err)
	writeAgePack(t, root, "default", ciphertext)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\n")

	ctx := loadCtx(t, root, path)
	ctx.Cfg.Services = appServices()

	require.Empty(t, (&unresolvedValidator{}).Run(ctx))
}

func TestUnresolvedValidator_ageSourceWithoutIdentityNamesEveryFile(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	noIdentity(t)
	ciphertext, err := secrets.EncryptBytes([]byte("creds"), id.Recipient())
	require.NoError(t, err)
	writeAgePack(t, root, "default", ciphertext)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\n")

	ctx := loadCtx(t, root, path)
	ctx.Cfg.Services = appServices()

	diags := (&unresolvedValidator{}).Run(ctx)
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, "secrets.unresolved:packs", diags[0].Target)
	require.Contains(t, diags[0].Message, "creds.json.age")
	require.Contains(t, diags[0].Hint, "dwe secrets key import")
}

// A disabled service never renders its config pack, so its .age sources are not
// a readiness problem — the validator mirrors DeployOrder's gate.
func TestUnresolvedValidator_disabledServiceSourceIgnored(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	stranger := mustKeygen(t)
	useIdentity(t, id)
	ciphertext, err := secrets.EncryptBytes([]byte("creds"), stranger.Recipient())
	require.NoError(t, err)
	writeAgePack(t, root, "default", ciphertext)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\n")

	ctx := loadCtx(t, root, path)
	ctx.Cfg.Services = map[string]config.ServiceConfig{
		"main": {Type: config.ServiceTypeApp, Enabled: false, Dir: "services/main"},
	}
	require.Empty(t, (&unresolvedValidator{}).Run(ctx))
}

func TestAll_domainAndIDs(t *testing.T) {
	got := map[string]string{}
	for _, v := range All() {
		got[v.ID()] = v.Domain()
	}
	require.Equal(t, map[string]string{"recipient": "secrets", "unresolved": "secrets"}, got)

	only := UnresolvedValidator()
	require.Equal(t, "secrets", only.Domain())
	require.Equal(t, "unresolved", only.ID())
}
