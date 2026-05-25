package setup

// Question type constants
const (
	TypeInput       = "input"
	TypeSelect      = "select"
	TypeMultiselect = "multiselect"
	TypeConfirm     = "confirm"
)

// SetupConfig represents the parsed setup.yml configuration
type SetupConfig struct {
	Questions []Question `yaml:"questions"`
}

// Question represents a single question in the setup configuration
type Question struct {
	ID          string        `yaml:"id"`
	Type        string        `yaml:"type"`
	Title       string        `yaml:"title"`
	Description string        `yaml:"description"`
	Required    bool          `yaml:"required"`
	Writes      string        `yaml:"writes"`
	Options     []Option      `yaml:"options"`
	Validate    *ValidateSpec `yaml:"validate"`
}

// Option represents a single option for select/multiselect questions
type Option struct {
	Value string `yaml:"value"`
	Label string `yaml:"label"`
}

// ValidateSpec represents validation configuration for a question
type ValidateSpec struct {
	Preset string `yaml:"preset"`
	Regex  string `yaml:"regex"`
}
