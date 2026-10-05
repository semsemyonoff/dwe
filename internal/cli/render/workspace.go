package render

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/manifest"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	workspacepkg "github.com/semsemyonoff/dwe/internal/core/execution/templates/workspace"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

func newWorkspaceCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "workspace [pack…]",
		Short: "Render workspace template packs into the project root",
		Long: `Render packs from workspace/templates/workspace/<pack>/ into the project root.

Explicit pack names replace the render.workspace list for this invocation.
With no arguments, render the configured list; an empty list does nothing.
Every pack must exist. Only sources ending in .tmpl are Go templates; other
sources are copied verbatim. File permissions follow the source.

All packs are validated and rendered in memory before any files are written.
Protected project paths and colliding destinations are rejected.`,
		Example: `  dwe render workspace
  dwe render workspace ralphex root-agents`,
		SilenceUsage:      true,
		ValidArgsFunction: workspacePackCompletion(flags),
		RunE: func(_ *cobra.Command, args []string) error {
			// Root artifacts may be tracked, so even a working age identity
			// must not expose plaintext secrets to pack templates.
			cfg, err := config.LoadConfigSanitizedOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				names = cfg.Render.Workspace
			}
			w := render.Stdout()
			if len(names) == 0 {
				w.Info("no workspace packs configured in render.workspace")
				return nil
			}
			commands, groups := loadCommandIndex(w, flags.ConfigPath)
			data := packcommon.TemplateData{
				Project:       cfg.Project,
				Runtime:       cfg.Runtime,
				Services:      cfg.Services,
				Cfg:           cfg,
				Commands:      commands,
				CommandGroups: groups,
			}
			results, err := workspacepkg.Render(flags.ProjectRoot(), names, data)
			if err != nil {
				return err
			}
			for _, result := range results {
				for _, dest := range result.Files {
					if slices.Contains(result.OverrideHits, dest) {
						w.Info(fmt.Sprintf("using local override: workspace [%s] → %s", result.Name, dest))
					}
					w.Success(fmt.Sprintf("workspace [%s] → %s", result.Name, dest))
				}
				for _, link := range result.Symlinks {
					w.Success(fmt.Sprintf("workspace [%s] → %s (symlink)", result.Name, link))
				}
			}
			return nil
		},
	}
}

func workspacePackCompletion(flags *cmdctx.RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		configPath, projectRoot, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if _, err := config.LoadConfigSanitizedOrWrap(configPath); err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		packDir := filepath.Join(projectRoot, "workspace", "templates", "workspace")
		if err := pathsafe.CheckNoSymlinks(projectRoot, packDir, "workspace packs"); err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		entries, err := os.ReadDir(packDir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || manifest.ValidatePackName(name) != nil ||
				!strings.HasPrefix(name, toComplete) || slices.Contains(args, name) {
				continue
			}
			names = append(names, name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
