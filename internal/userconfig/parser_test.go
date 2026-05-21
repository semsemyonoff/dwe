package userconfig

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
	assert.Contains(t, err.Error(), "malformed")
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

func TestParse_Empty(t *testing.T) {
	cfg := Defaults()
	require.NoError(t, parse(strings.NewReader(""), cfg))
	assert.True(t, cfg.NotifyEnabled)
	assert.Equal(t, []string{"native"}, cfg.NotifyChannels)
}
