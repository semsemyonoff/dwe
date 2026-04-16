package command

import (
	"fmt"
	"path/filepath"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"

	"github.com/spf13/cobra"
)

// defaultDefinitionIndent is applied when a definition item has no explicit indent set.
const defaultDefinitionIndent = 2

func newInfoCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Display project info, URLs, credentials and available commands",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(flags)
		},
		SilenceUsage: true,
	}
}

func runInfo(flags *rootFlags) error {
	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	infoPath := filepath.Join(filepath.Dir(flags.configPath), "devbox", "info.yml")
	infoCfg, err := config.LoadInfoConfig(infoPath)
	if err != nil {
		return fmt.Errorf("loading devbox/info.yml: %w", err)
	}

	return renderInfo(render.Stdout(), cfg, infoCfg)
}

// renderInfo renders the full project info screen to w.
func renderInfo(w *render.Writer, cfg *config.DevboxConfig, infoCfg *config.InfoConfig) error {
	if infoCfg.Settings.LineWidth > 0 {
		w.SetLineWidth(infoCfg.Settings.LineWidth)
	}

	// ASCII art header
	if len(infoCfg.Header.ASCII.Lines) > 0 {
		if err := w.ASCII(infoCfg.Header.ASCII.Lines, infoCfg.Header.ASCII.Font, infoCfg.Header.ASCII.Color); err != nil {
			return fmt.Errorf("header ASCII: %w", err)
		}
	}

	for _, section := range infoCfg.Sections {
		if section.Title != "" {
			w.TableHeader(section.Title)
		}

		for _, item := range section.Items {
			show, err := tpl.EvalCondition(item.When, cfg)
			if err != nil {
				return fmt.Errorf("section %q item %q when: %w", section.ID, item.Type, err)
			}
			if !show {
				continue
			}

			if err := renderItem(w, cfg, item); err != nil {
				return fmt.Errorf("section %q: %w", section.ID, err)
			}
		}
	}

	if infoCfg.Footer {
		w.TableHeader("")
	}

	return nil
}

func renderItem(w *render.Writer, cfg *config.DevboxConfig, item config.InfoItem) error {
	switch item.Type {
	case "subheader":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return err
		}
		w.TableSubheader(text)

	case "definition":
		value, err := tpl.Render(item.Value, cfg)
		if err != nil {
			return err
		}
		indent := defaultDefinitionIndent
		if item.Indent.IsSet() {
			indent = item.Indent.Value()
		}
		w.Definition(item.Name, value, indent, item.Icon)

	case "warning":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return err
		}
		w.Warning(text)

	case "info":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return err
		}
		indent := strings.Repeat(" ", item.Indent.Value())
		w.Info(indent + text)

	case "separator":
		w.Println("")

	default:
		// Unknown types are silently skipped to allow forward compatibility.
	}
	return nil
}
