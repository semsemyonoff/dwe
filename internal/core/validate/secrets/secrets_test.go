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
	// A valid recipient next to a damaged marker must NOT be affirmed: the row
	// would sit next to the error and read as "the setup is fine".
	require.Len(t, diags, 1, "a damaged payload is diagnosable without any key: %s", messages(diags))
	require.Contains(t, diags[0].Message, "vars.token")
	require.Contains(t, diags[0].Message, "damaged")
	require.Equal(t, "secrets.marker:vars.token", diags[0].Target)
}

func TestRecipientValidator_validSetupEmitsOK(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	marker := mustEncrypt(t, "s3cr3t", id.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+marker+"\n")

	diags := (&recipientValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, validate.SeverityOK, diags[0].Severity)
	require.Equal(t, "secrets", diags[0].Domain)
	require.Equal(t, "secrets.recipient", diags[0].Target)
	require.Equal(t, "workspace.yml", diags[0].File)
	require.Empty(t, diags[0].Message)
	require.Empty(t, diags[0].Hint)
}

// The OK row is tied to the recipient, not to the presence of secrets: a
// project that declares a recipient but has nothing encrypted yet (the state
// right after `dwe secrets init`) still gets the positive row.
func TestRecipientValidator_recipientWithoutSecretsEmitsOK(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\n")

	diags := (&recipientValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, validate.SeverityOK, diags[0].Severity)
	require.Equal(t, "secrets.recipient", diags[0].Target)
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

// A truncated DWE_AGE_KEY is a BROKEN source, not a missing one: reporting it
// as no_identity sends the reader looking for a key they already have.
func TestUnresolvedValidator_invalidIdentity(t *testing.T) {
	root := t.TempDir()
	project := mustKeygen(t)
	truncated := project.Export()[:len(project.Export())-10]
	noIdentity(t)
	t.Setenv(secrets.EnvKey, truncated)
	marker := mustEncrypt(t, "s3cr3t", project.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+project.Recipient()+
		"\nvars:\n  token: "+marker+"\n  other: "+mustEncrypt(t, "second", project.Recipient())+"\n")

	ctx := loadCtx(t, root, path)
	require.NotNil(t, ctx.Cfg)

	diags := (&unresolvedValidator{}).Run(ctx)
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, "secrets.unresolved:invalid_identity", diags[0].Target)
	require.Contains(t, diags[0].Message, "$"+secrets.EnvKey+" is set but holds no age identity")
	require.Contains(t, diags[0].Message, "2 encrypted value(s)")
	require.Equal(t, secrets.IdentityHint(project.Recipient()), diags[0].Hint)
	// The broken key text is a private key with 10 characters lopped off; no
	// surface may echo it, and the age parse error would.
	require.NotContains(t, messages(diags), truncated[len(truncated)-20:])
	require.NotContains(t, messages(diags), "AGE-SECRET-KEY-")
}

// The env-file source gets its own fixed phrase: "unset DWE_AGE_KEY" is the
// wrong fix when the file is the broken one.
func TestUnresolvedValidator_invalidIdentityFromEnvFile(t *testing.T) {
	root := t.TempDir()
	project := mustKeygen(t)
	noIdentity(t)
	keyfile := filepath.Join(t.TempDir(), "broken.key")
	require.NoError(t, os.WriteFile(keyfile, []byte("# public key: nothing useful\n"), 0o600))
	t.Setenv(secrets.EnvKeyFile, keyfile)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+project.Recipient()+
		"\nvars:\n  token: "+mustEncrypt(t, "s3cr3t", project.Recipient())+"\n")

	diags := (&unresolvedValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, "secrets.unresolved:invalid_identity", diags[0].Target)
	require.Contains(t, diags[0].Message, "$"+secrets.EnvKeyFile+" is set but the file it points at holds no age identity")
}

// With no env source set the phrase blames the keyfile — the only source left.
func TestReasonPhrase_invalidIdentityFallsBackToKeyfile(t *testing.T) {
	noIdentity(t)
	got := reasonPhrase(config.ReasonInvalidIdentity, "age1whatever", "")
	require.Equal(t, "the keyfile on this machine holds no age identity", got)
}

// A marker-only project is the case the pre-restructure early return skipped
// entirely: it returned before any source was known, so the OK row was
// unreachable however healthy the project was.
func TestUnresolvedValidator_decryptedEmitsOK(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	marker := mustEncrypt(t, "s3cr3t", id.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+marker+"\n")

	ctx := loadCtx(t, root, path)
	require.Equal(t, "s3cr3t", ctx.Cfg.Vars["token"])

	diags := (&unresolvedValidator{}).Run(ctx)
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, validate.SeverityOK, diags[0].Severity)
	require.Equal(t, "secrets", diags[0].Domain)
	require.Equal(t, "secrets.unresolved", diags[0].Target)
	require.Equal(t, "workspace.yml", diags[0].File)
	require.Equal(t, "1 encrypted value(s) and 0 config-pack source(s) readable via env", diags[0].Message)
	require.NotContains(t, messages(diags), "s3cr3t")
	require.NotContains(t, messages(diags), "AGE-SECRET-KEY-")
}

// A project with no secrets: block and nothing encrypted stays silent, so
// `dwe validate` output for such projects is unchanged.
func TestUnresolvedValidator_noSecretsIsSilent(t *testing.T) {
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nvars:\n  plain: value\n")

	require.Empty(t, (&unresolvedValidator{}).Run(loadCtx(t, root, path)))
}

// Mixed state: the recipient is fine, one marker is not readable. The positive
// row belongs to the recipient validator only — readiness failed.
func TestUnresolvedValidator_mixedStateHasNoOKRow(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	noIdentity(t)
	marker := mustEncrypt(t, "s3cr3t", id.Recipient())
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+marker+"\n")

	ctx := loadCtx(t, root, path)

	recipientDiags := (&recipientValidator{}).Run(ctx)
	require.Len(t, recipientDiags, 1, messages(recipientDiags))
	require.Equal(t, validate.SeverityOK, recipientDiags[0].Severity)
	require.Equal(t, "secrets.recipient", recipientDiags[0].Target)

	unresolvedDiags := (&unresolvedValidator{}).Run(ctx)
	require.Len(t, unresolvedDiags, 1, messages(unresolvedDiags))
	require.Equal(t, validate.SeverityError, unresolvedDiags[0].Severity)
	require.Equal(t, "secrets.unresolved:no_identity", unresolvedDiags[0].Target)
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

// An .age-only project carries no marker, so the load pass never looked for an
// identity and SecretsState.IdentitySource is empty: the source word in the OK
// row comes from this validator's own LoadIdentity call.
func TestUnresolvedValidator_ageSourceDecryptableEmitsOK(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)
	ciphertext, err := secrets.EncryptBytes([]byte("creds"), id.Recipient())
	require.NoError(t, err)
	writeAgePack(t, root, "default", ciphertext)
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\n")

	ctx := loadCtx(t, root, path)
	ctx.Cfg.Services = appServices()
	require.Empty(t, ctx.Cfg.SecretsState.IdentitySource, "a marker-free load must not have loaded an identity")

	diags := (&unresolvedValidator{}).Run(ctx)
	require.Len(t, diags, 1, messages(diags))
	require.Equal(t, validate.SeverityOK, diags[0].Severity)
	require.Equal(t, "secrets.unresolved", diags[0].Target)
	require.Equal(t, "0 encrypted value(s) and 1 config-pack source(s) readable via env", diags[0].Message)
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
	require.Equal(t, map[string]string{
		"recipient":  "secrets",
		"unresolved": "secrets",
		"shadowed":   "secrets",
	}, got)

	// The cherry-pick stays a single validator: adding a content validator to
	// the domain must never start blocking lifecycle commands.
	only := UnresolvedValidator()
	require.Equal(t, "secrets", only.Domain())
	require.Equal(t, "unresolved", only.ID())
}

// writeLocalLayer writes workspace/local.yml, the shadowing layer in every test
// below.
func writeLocalLayer(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "local.yml"), []byte(body), 0o644))
}

// A project with no encrypted values gains no row — and pays no identity
// lookup, which is what keeps `dwe validate` in a secretless project unchanged.
func TestShadowedValidator_silentWithoutMarkers(t *testing.T) {
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nvars:\n  token: plain\n")
	require.Empty(t, (&shadowedValidator{}).Run(loadCtx(t, root, path)))
}

// The positive row is the point: a green `dwe validate secrets` has to mean
// "the key pair is what shares these values", not only "the markers decrypt".
func TestShadowedValidator_okRowWhenNothingIsShadowed(t *testing.T) {
	id := mustKeygen(t)
	useIdentity(t, id)
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+mustEncrypt(t, "s3cret", id.Recipient())+"\n")

	diags := (&shadowedValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityOK, diags[0].Severity)
	require.Equal(t, "secrets.shadowed", diags[0].Target)
	require.Contains(t, diags[0].Message, "none shadowed")
}

// The two verdicts get different wording and share one severity: a leftover copy
// is a migration nobody finished, a different value is a deliberate override,
// and neither is an error — breaking a legitimate local override would cost more
// than the visibility buys.
func TestShadowedValidator_identicalAndDifferentAreSeparateWarnings(t *testing.T) {
	id := mustKeygen(t)
	useIdentity(t, id)
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+mustEncrypt(t, "s3cret", id.Recipient())+
		"\n  other: "+mustEncrypt(t, "shared", id.Recipient())+"\n")
	writeLocalLayer(t, root, "vars:\n  token: s3cret\n  other: mine\n")

	diags := (&shadowedValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 2)

	byTarget := map[string]validate.Diagnostic{}
	for _, d := range diags {
		require.Equal(t, validate.SeverityWarning, d.Severity)
		require.Equal(t, filepath.Join("workspace", "local.yml"), d.File)
		byTarget[d.Target] = d
	}

	identical := byTarget["secrets.shadowed:identical"]
	require.Contains(t, identical.Message, "vars.token")
	require.Contains(t, identical.Message, "identical plaintext value")
	require.Contains(t, identical.Hint, "already holds the same content")

	different := byTarget["secrets.shadowed:different"]
	require.Contains(t, different.Message, "vars.other")
	require.Contains(t, different.Message, "different plaintext value")
	require.Contains(t, different.Hint, "local override")

	// Neither side of the comparison reaches the report.
	require.NotContains(t, messages(diags), "s3cret")
	require.NotContains(t, messages(diags), "mine")
}

// No key here plus a plaintext quietly covering for it is the pairing that keeps
// a lost identity invisible in everyday use, so the row leads with the identity
// rather than claiming a comparison it could not make.
func TestShadowedValidator_undecryptableMarkerLeadsWithTheIdentity(t *testing.T) {
	id := mustKeygen(t)
	noIdentity(t)
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
		"\nvars:\n  token: "+mustEncrypt(t, "s3cret", id.Recipient())+"\n")
	writeLocalLayer(t, root, "vars:\n  token: s3cret\n")

	diags := (&shadowedValidator{}).Run(loadCtx(t, root, path))
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityWarning, diags[0].Severity)
	require.Equal(t, "secrets.shadowed:unknown", diags[0].Target)
	require.Contains(t, diags[0].Message, "overridden by a plaintext value")
	require.Contains(t, diags[0].Hint, "were not compared")
	require.Contains(t, diags[0].Hint, "dwe secrets key import")
}

// A malformed recipient makes LoadConfig fail; the validator raw-loads for the
// same reason the recipient validator does, so a scoped run is not blinded by a
// nil Cfg — and it stays silent rather than duplicating that diagnosis.
func TestShadowedValidator_survivesAFailedConfigLoad(t *testing.T) {
	root := t.TempDir()
	path := writeProject(t, root, "project:\n  name: test\nsecrets:\n  recipient: age1-not-a-real-key\n")
	require.Empty(t, (&shadowedValidator{}).Run(loadCtx(t, root, path)))
}
