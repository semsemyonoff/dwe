package user

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_CommentsAndBlankLines(t *testing.T) {
	in := `
# top comment

notify_enabled = true
   # indented comment

notify_run_enabled=false
`
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.True(t, cfg.NotifyEnabled)
	assert.False(t, cfg.NotifyRunEnabled)
}

func TestParse_ListAndWhitespace(t *testing.T) {
	in := "notify_channels =  native ,  telegram , webhook \n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Equal(t, []string{"native", "telegram", "webhook"}, cfg.NotifyChannels)
}

func TestParse_EmptyListValue(t *testing.T) {
	in := "notify_channels = \n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Empty(t, cfg.NotifyChannels)
}

func TestParse_InlineCommentRejected(t *testing.T) {
	in := "notify_enabled = true # nope\n"
	cfg := Defaults()
	err := parse(strings.NewReader(in), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inline comments")
}

func TestParse_DottedKeyRejected(t *testing.T) {
	in := "notify.enabled = true\n"
	cfg := Defaults()
	err := parse(strings.NewReader(in), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dotted keys not allowed")
}

func TestParse_InvalidBoolean(t *testing.T) {
	in := "notify_enabled = maybe\n"
	cfg := Defaults()
	err := parse(strings.NewReader(in), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid boolean")
}

func TestParse_BoolVariants(t *testing.T) {
	cases := map[string]bool{
		"1": true, "true": true, "yes": true, "TRUE": true,
		"0": false, "false": false, "no": false, "No": false,
	}
	for v, want := range cases {
		cfg := Defaults()
		err := parse(strings.NewReader("notify_enabled = "+v+"\n"), cfg)
		require.NoError(t, err, "value %q", v)
		assert.Equal(t, want, cfg.NotifyEnabled, "value %q", v)
	}
}

func TestParse_UnknownKeyIsWarning(t *testing.T) {
	in := "notify_future_thing = whatever\n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	// Defaults preserved.
	assert.True(t, cfg.NotifyEnabled)
}

func TestParse_MalformedLine(t *testing.T) {
	in := "no equals sign here\n"
	cfg := Defaults()
	err := parse(strings.NewReader(in), cfg)
	require.Error(t, err)
}

func TestParse_ReservedFields(t *testing.T) {
	in := `notify_telegram_token = abc123
notify_telegram_chat = -100
notify_webhook_urls = https://a.example, https://b.example
`
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Equal(t, "abc123", cfg.notifyTelegramToken)
	assert.Equal(t, "-100", cfg.notifyTelegramChat)
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, cfg.notifyWebhookURLs)
}

func TestParse_HashInValueAllowed(t *testing.T) {
	// Bare '#' without a preceding space is a URL fragment, not an inline
	// comment — must be accepted.
	in := "notify_webhook_urls = https://example.com#section\n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Equal(t, []string{"https://example.com#section"}, cfg.notifyWebhookURLs)
}

func TestParse_Empty(t *testing.T) {
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(""), cfg))
	assert.True(t, cfg.NotifyEnabled)
	assert.Equal(t, []string{"native"}, cfg.NotifyChannels)
}

func TestParse_Language(t *testing.T) {
	in := "language = ru\n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Equal(t, "ru", cfg.Language)
}

func TestParse_LanguageEmpty(t *testing.T) {
	in := "language = \n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Equal(t, "", cfg.Language)
}

func TestParse_LanguageAnyValue(t *testing.T) {
	// Language accepts any string value; validation happens in the resolver.
	in := "language = ru_RU.UTF-8\n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Equal(t, "ru_RU.UTF-8", cfg.Language)
}

func TestParse_BinaryOverrides(t *testing.T) {
	in := `binary_shellcheck = /usr/local/bin/shellcheck
binary_hadolint = /custom/hadolint
`
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	assert.Equal(t, "/usr/local/bin/shellcheck", cfg.Binaries["shellcheck"])
	assert.Equal(t, "/custom/hadolint", cfg.Binaries["hadolint"])
}

func TestParse_BinaryOverrideWithWhitespace(t *testing.T) {
	in := "binary_docker =  /opt/bin/docker  \n"
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(in), cfg))
	// Parser trims whitespace from values at parse time
	assert.Equal(t, "/opt/bin/docker", cfg.Binaries["docker"])
}

func TestBinaryOverride(t *testing.T) {
	cfg := &Config{
		Binaries: map[string]string{
			"shellcheck": "/usr/local/bin/shellcheck",
			"hadolint":   "  /custom/hadolint  ",
		},
	}

	// Present override returns trimmed value and true
	path, ok := cfg.BinaryOverride("shellcheck")
	assert.True(t, ok)
	assert.Equal(t, "/usr/local/bin/shellcheck", path)

	// Whitespace is trimmed
	path, ok = cfg.BinaryOverride("hadolint")
	assert.True(t, ok)
	assert.Equal(t, "/custom/hadolint", path)

	// Missing override returns false
	_, ok = cfg.BinaryOverride("missing")
	assert.False(t, ok)

	// Nil config returns false
	var nilCfg *Config
	_, ok = nilCfg.BinaryOverride("anything")
	assert.False(t, ok)
}
