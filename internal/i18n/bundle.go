package i18n

// Bundle is the parsed contents of one translation file.
type Bundle struct {
	UI       map[string]string `yaml:"ui"`
	Commands map[string]CommandStrings `yaml:"commands"`
	Groups   map[string]GroupStrings `yaml:"groups"`
}

// CommandStrings contains translations for a command's user-facing text.
type CommandStrings struct {
	Description      string                 `yaml:"description"`
	ConfirmationText string                 `yaml:"confirmation_text"`
	Params           map[string]ParamStrings `yaml:"params"`
}

// ParamStrings contains translations for a parameter.
type ParamStrings struct {
	Description string `yaml:"description"`
}

// GroupStrings contains translations for a command group.
type GroupStrings struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

// ProjectFile represents one parsed translation file from the project layer.
type ProjectFile struct {
	Path     string // absolute path; "" for directory-level failures
	Locale   string // 2-letter code; "" for directory-level failures
	Bundle   *Bundle
	ParseErr error
}
