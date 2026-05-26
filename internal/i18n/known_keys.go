package i18n

// KnownUIKeys is the authoritative list of valid ui.* keys.
// This must stay in sync with internal/i18n/translations/en.yml.
// Used by the validator to flag typo'd keys like "ui.docs.section.properies".
var KnownUIKeys = []string{
	"docs.section.properties",
	"docs.section.command",
	"docs.section.parameters",
	"docs.section.context",
	"docs.section.environment",
	"docs.section.with",
	"docs.section.script",
	"docs.section.argv",
	"docs.section.files",
	"docs.property.id",
	"docs.property.type",
	"docs.property.group",
	"docs.property.private",
	"docs.property.confirmation",
	"docs.property.confirmation_text",
	"docs.property.success_message",
	"docs.property.error_message",
	"docs.property.shell",
	"docs.property.service",
	"docs.property.workdir",
	"docs.property.builtin",
	"docs.property.compose_args",
	"docs.property.script",
}
