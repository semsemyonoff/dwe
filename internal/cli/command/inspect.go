package command

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// commandInspectJSON is the DTO for `commands [id] --inspect --output json`.
type commandInspectJSON struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Description      string            `json:"description,omitempty"`
	Private          bool              `json:"private,omitempty"`
	Hidden           bool              `json:"hidden,omitempty"`
	Hide             string            `json:"hide,omitempty"`
	Confirmation     bool              `json:"confirmation,omitempty"`
	ConfirmationText string            `json:"confirmation_text,omitempty"`
	DerivedFrom      string            `json:"derived_from,omitempty"`
	Cmd              string            `json:"cmd,omitempty"`
	Argv             []string          `json:"argv,omitempty"`
	Service          string            `json:"service,omitempty"`
	User             string            `json:"user,omitempty"`
	Workdir          string            `json:"workdir,omitempty"`
	WorkdirFrom      string            `json:"workdir_from,omitempty"`
	Mode             string            `json:"mode,omitempty"`
	ComposeArgs      []string          `json:"compose_args,omitempty"`
	Script           *scriptDefJSON    `json:"script,omitempty"`
	Steps            []stepJSON        `json:"steps,omitempty"`
	With             map[string]any    `json:"with,omitempty"`
	DaemonSpec       *daemonSpecJSON   `json:"daemon_spec,omitempty"`
	Params           []paramEntryJSON  `json:"params,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Messages         *messagesJSON     `json:"messages,omitempty"`
}

type scriptDefJSON struct {
	Shell   string `json:"shell,omitempty"`
	Path    string `json:"path,omitempty"`
	Plan    string `json:"plan,omitempty"`
	Run     string `json:"run,omitempty"`
	Cleanup string `json:"cleanup,omitempty"`
}

type stepJSON struct {
	Kind            string            `json:"kind"`
	Command         string            `json:"command,omitempty"`
	Confirm         string            `json:"confirm,omitempty"`
	When            string            `json:"when,omitempty"`
	ContinueOnError bool              `json:"continue_on_error,omitempty"`
	With            map[string]string `json:"with,omitempty"`
	Parallel        *parallelJSON     `json:"parallel,omitempty"`
}

type parallelJSON struct {
	MaxConcurrent int        `json:"max_concurrent,omitempty"`
	FailFast      *bool      `json:"fail_fast,omitempty"`
	Steps         []stepJSON `json:"steps,omitempty"`
}

type daemonSpecJSON struct {
	ContainerTemplate string `json:"container_template,omitempty"`
	OnAlreadyRunning  string `json:"on_already_running,omitempty"`
	AutoRemove        *bool  `json:"auto_remove,omitempty"`
	StopTimeout       string `json:"stop_timeout,omitempty"`
}

type messagesJSON struct {
	Success string `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

// scriptShell returns s.Shell if non-empty, otherwise the portable default "sh".
// Used wherever a ScriptDef's effective shell is displayed or serialised.
func scriptShell(s *usercommands.ScriptDef) string {
	if s.Shell != "" {
		return s.Shell
	}
	return "sh"
}

// buildCommandInspectJSON converts a CommandDef to its JSON inspect representation.
func buildCommandInspectJSON(def *usercommands.CommandDef, translator i18n.Translator, locale string) commandInspectJSON {
	data := commandInspectJSON{
		ID:          def.ID,
		Type:        string(def.Type),
		Description: translator.CommandDescription(locale, def.ID, def.Description),
		Private:     def.Private,
		Hidden:      def.Hidden,
		Hide:        def.Hide,
		DerivedFrom: def.DerivedFromDaemon,
	}
	if def.Confirmation {
		data.Confirmation = true
		data.ConfirmationText = translator.CommandConfirmationText(locale, def.ID, def.EffectiveConfirmationText())
	}
	if def.Messages.Success != "" || def.Messages.Error != "" {
		data.Messages = &messagesJSON{
			Success: def.Messages.Success,
			Error:   def.Messages.Error,
		}
	}

	switch def.Type {
	case usercommands.CommandTypeShell, usercommands.CommandTypeDwe:
		data.Cmd = def.Cmd
		data.Argv = def.Argv
		data.Workdir = def.Workdir
	case usercommands.CommandTypeServiceExec, usercommands.CommandTypeServiceRun:
		data.Service = def.Service
		data.User = string(def.User)
		data.Workdir = def.Workdir
		data.WorkdirFrom = def.WorkdirFrom
		data.Mode = string(def.Mode)
		data.ComposeArgs = def.ComposeArgs
		data.Cmd = def.Cmd
		data.Argv = def.Argv
	case usercommands.CommandTypeScript:
		if def.Script != nil {
			data.Script = &scriptDefJSON{
				Shell:   scriptShell(def.Script),
				Path:    def.Script.Path,
				Plan:    def.Script.Plan,
				Run:     def.Script.Run,
				Cleanup: def.Script.Cleanup,
			}
		}
		data.Workdir = def.Workdir
	case usercommands.CommandTypeBuiltin:
		data.Cmd = def.Cmd
		if len(def.With) > 0 {
			data.With = def.With
		}
	case usercommands.CommandTypeWorkflow:
		data.Steps = buildStepsJSON(def.Steps)
	}

	if def.DerivedFromDaemon != "" && def.SourceDaemon != nil {
		ds := def.SourceDaemon
		data.DaemonSpec = &daemonSpecJSON{
			ContainerTemplate: ds.ContainerTemplate,
			OnAlreadyRunning:  ds.OnAlreadyRunning,
			AutoRemove:        ds.AutoRemove,
			StopTimeout:       ds.StopTimeout,
		}
	}

	data.Params = buildParamEntriesJSON(def, translator, locale)

	if len(def.Env) > 0 {
		data.Env = def.Env
	}

	return data
}

// buildStepsJSON converts workflow steps to their JSON representation.
func buildStepsJSON(steps []usercommands.WorkflowStep) []stepJSON {
	result := make([]stepJSON, 0, len(steps))
	for _, step := range steps {
		s := stepJSON{
			When:            step.When,
			ContinueOnError: step.ContinueOnError,
		}
		switch {
		case step.Confirm != "":
			s.Kind = "confirm"
			s.Confirm = step.Confirm
		case step.Parallel != nil:
			s.Kind = "parallel"
			p := step.Parallel
			s.Parallel = &parallelJSON{
				MaxConcurrent: p.MaxConcurrent,
				FailFast:      p.FailFast,
				Steps:         buildStepsJSON(p.Steps),
			}
		default:
			s.Kind = "command"
			s.Command = step.Command
			if len(step.With) > 0 {
				s.With = step.With
			}
		}
		result = append(result, s)
	}
	return result
}

// inspectStepDescription returns the localized description for the command
// referenced by a workflow step, formatted with a leading em-dash separator so
// it can be concatenated onto a definition value. Returns "" when the command
// is unknown or carries no description.
func inspectStepDescription(reg *usercommands.Registry, translator i18n.Translator, locale, commandID string) string {
	if reg == nil || commandID == "" {
		return ""
	}
	target, err := reg.Get(commandID)
	if err != nil {
		return ""
	}
	desc := translator.CommandDescription(locale, target.ID, target.Description)
	if desc == "" {
		return ""
	}
	return " — " + desc
}

// printInspect writes a detailed view of a command definition using Lipgloss styles.
// cfg may be nil at call sites that exercise the renderer purely structurally
// (tests); the resolved container-name block is then omitted.
//
// Word-wrap follows the terminal width. For renderings into a fixed sub-region
// (e.g. an inspect viewport narrower than the terminal), use
// [printInspectAt] with the explicit width — otherwise values wrap to the
// terminal and get silently clipped when the viewport renders.
func printInspect(w io.Writer, def *usercommands.CommandDef, cfg *config.DweConfig, reg *usercommands.Registry, translator i18n.Translator, locale, baseDir string) {
	printInspectAt(w, def, cfg, reg, 0, translator, locale, baseDir)
}

// printInspectAt is [printInspect] with an explicit wrap width. maxWidth == 0
// falls back to the terminal width. baseDir is the project root used to resolve
// the daemon container name honoring docker.yml project_name; "" degrades to
// cfg.Project.FullName().
func printInspectAt(w io.Writer, def *usercommands.CommandDef, cfg *config.DweConfig, reg *usercommands.Registry, maxWidth int, translator i18n.Translator, locale, baseDir string) {
	def2 := func(name, value string, indent int) {
		_, _ = fmt.Fprintln(w, render.DefinitionAt(name, value, indent, "", maxWidth))
	}
	sub := func(title string) {
		_, _ = fmt.Fprintln(w, render.Subheader("  "+title))
	}

	_, _ = fmt.Fprintln(w, render.SectionTitle(def.ID))
	def2("type", string(def.Type), 2)
	if def.DerivedFromDaemon != "" {
		def2("derived from", "daemon "+def.DerivedFromDaemon, 2)
	}
	desc := translator.CommandDescription(locale, def.ID, def.Description)
	if desc != "" {
		def2("description", desc, 2)
	}
	if def.Private {
		def2("private", "true", 2)
	}
	if def.Hide != "" {
		def2("hide", def.Hide, 2)
	}
	if def.Hidden {
		def2("hidden", "true (resolved by hide: condition or cascaded from a hidden parent group)", 2)
	}
	if def.Confirmation {
		def2("confirmation", "true", 2)
		confirmText := translator.CommandConfirmationText(locale, def.ID, def.EffectiveConfirmationText())
		def2("confirmation_text", confirmText, 2)
	}
	if def.Messages.Success != "" || def.Messages.Error != "" {
		sub("Messages")
		if def.Messages.Success != "" {
			def2("success", def.Messages.Success, 4)
		}
		if def.Messages.Error != "" {
			def2("error", def.Messages.Error, 4)
		}
	}

	switch def.Type {
	case usercommands.CommandTypeDwe:
		if def.Cmd != "" {
			def2("cmd", def.Cmd, 2)
		}
	case usercommands.CommandTypeShell:
		if def.Cmd != "" {
			def2("cmd", def.Cmd, 2)
		}
		if len(def.Argv) > 0 {
			def2("argv", strings.Join(def.Argv, " "), 2)
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
	case usercommands.CommandTypeServiceExec, usercommands.CommandTypeServiceRun:
		if def.Service != "" {
			def2("service", def.Service, 2)
		}
		if def.Runner != nil && def.Runner.Service != "" {
			def2("service (runner)", def.Runner.Service, 2)
		}
		if def.User != "" {
			def2("user", string(def.User), 2)
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
		if def.WorkdirFrom != "" {
			def2("workdir_from", def.WorkdirFrom, 2)
		}
		if def.Mode != "" {
			def2("mode", string(def.Mode), 2)
		}
		if len(def.ComposeArgs) > 0 {
			def2("compose_args", strings.Join(def.ComposeArgs, " "), 2)
		}
		if def.Cmd != "" {
			def2("cmd", def.Cmd, 2)
		}
		if len(def.Argv) > 0 {
			def2("argv", strings.Join(def.Argv, " "), 2)
		}
	case usercommands.CommandTypeScript:
		if def.Script != nil {
			def2("script.shell", scriptShell(def.Script), 2)
			if def.Script.Path != "" {
				def2("script.path", def.Script.Path, 2)
			}
			if def.Script.Plan != "" {
				def2("script.plan", def.Script.Plan, 2)
			}
			if def.Script.Run != "" {
				def2("script.run", def.Script.Run, 2)
			}
			if def.Script.Cleanup != "" {
				def2("script.cleanup", def.Script.Cleanup, 2)
			}
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
	case usercommands.CommandTypeBuiltin:
		if def.Cmd != "" {
			def2("builtin", def.Cmd, 2)
		}
		if len(def.With) > 0 {
			sub("With")
			var keys []string
			for k := range def.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				def2(k, fmt.Sprintf("%v", def.With[k]), 4)
			}
		}
	case usercommands.CommandTypeWorkflow:
		inspectWorkflowSteps(def2, sub, def, reg, translator, locale)
	}

	inspectDaemonSection(def2, sub, def, cfg, baseDir)
	inspectParamsSection(def2, sub, def, translator, locale)
	inspectContextSection(def2, sub, def)
	inspectEnvSection(def2, sub, def)
	inspectFilesSection(def2, sub, def)

	_, _ = fmt.Fprintln(w, render.SectionTitle(""))
}

// inspectDef2 writes a name/value definition line at the given indent.
type inspectDef2 = func(name, value string, indent int)

// inspectSub writes a sub-section header.
type inspectSub = func(title string)

// inspectWorkflowSteps renders the Steps section for a workflow command.
func inspectWorkflowSteps(def2 inspectDef2, sub inspectSub, def *usercommands.CommandDef, reg *usercommands.Registry, translator i18n.Translator, locale string) {
	sub("Steps")
	for i, step := range def.Steps {
		switch {
		case step.Confirm != "":
			def2(fmt.Sprintf("[%d] confirm", i), step.Confirm, 4)
		case step.Parallel != nil:
			p := step.Parallel
			label := fmt.Sprintf("[%d] parallel", i)
			var meta []string
			if p.MaxConcurrent > 0 {
				meta = append(meta, fmt.Sprintf("max_concurrent=%d", p.MaxConcurrent))
			}
			if p.FailFast != nil {
				meta = append(meta, fmt.Sprintf("fail_fast=%v", *p.FailFast))
			}
			desc := fmt.Sprintf("%d sub-steps", len(p.Steps))
			if len(meta) > 0 {
				desc += "  " + strings.Join(meta, ", ")
			}
			if step.When != "" {
				desc += "  when: " + step.When
			}
			def2(label, desc, 4)
			for j, sub := range p.Steps {
				subDesc := sub.Command + inspectStepDescription(reg, translator, locale, sub.Command)
				if sub.When != "" {
					subDesc += "  when: " + sub.When
				}
				if sub.ContinueOnError {
					subDesc += "  (continue_on_error)"
				}
				def2(fmt.Sprintf("  [%d.%d]", i, j), subDesc, 6)
			}
		default:
			label := fmt.Sprintf("[%d]", i)
			desc := step.Command + inspectStepDescription(reg, translator, locale, step.Command)
			if len(step.With) > 0 {
				var pairs []string
				for k, v := range step.With {
					pairs = append(pairs, k+"="+v)
				}
				sort.Strings(pairs)
				desc += "  with: " + strings.Join(pairs, ", ")
			}
			if step.When != "" {
				desc += "  when: " + step.When
			}
			if step.ContinueOnError {
				desc += "  (continue_on_error)"
			}
			def2(label, desc, 4)
		}
	}
}

// inspectDaemonSection renders the Daemon (and resolved Container) section for a
// synthetic daemon-derived command.
func inspectDaemonSection(def2 inspectDef2, sub inspectSub, def *usercommands.CommandDef, cfg *config.DweConfig, baseDir string) {
	if def.DerivedFromDaemon == "" || def.SourceDaemon == nil {
		return
	}
	sub("Daemon")
	ds := def.SourceDaemon
	def2("container_template", ds.ContainerTemplate, 4)
	if ds.OnAlreadyRunning != "" {
		def2("on_already_running", ds.OnAlreadyRunning, 4)
	}
	if ds.AutoRemove != nil {
		def2("auto_remove", fmt.Sprintf("%v", *ds.AutoRemove), 4)
	}
	if ds.StopTimeout != "" {
		def2("stop_timeout", ds.StopTimeout, 4)
	}
	// Execution fields live in def.With for synthetic commands (registry
	// expansion packs Service/User/Workdir/Argv/etc into the rendered map).
	if def.With != nil {
		withStr := func(key string) string {
			if v, ok := def.With[key]; ok {
				if s, ok := v.(string); ok {
					return s
				}
			}
			return ""
		}
		if s := withStr("service"); s != "" {
			def2("service", s, 4)
		}
		if s := withStr("user"); s != "" {
			def2("user", s, 4)
		}
		if s := withStr("workdir"); s != "" {
			def2("workdir", s, 4)
		}
		if s := withStr("workdir_from"); s != "" {
			def2("workdir_from", s, 4)
		}
		if argv, ok := def.With["argv"].([]any); ok && len(argv) > 0 {
			parts := make([]string, 0, len(argv))
			for _, a := range argv {
				if s, ok := a.(string); ok {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				def2("argv", strings.Join(parts, " "), 4)
			}
		}
	}
	if cfg != nil {
		sub("Container")
		defaults := make(map[string]any, len(def.Params))
		for name, p := range def.Params {
			defaults[name] = p.Default
		}
		rendered, err := tpl.RenderCommand(ds.ContainerTemplate, &tpl.RenderContext{
			Raw:    cfg.Raw,
			Params: defaults,
		})
		if err == nil {
			// Honor docker.yml project_name so the displayed name matches the
			// container the daemon builtins actually create. baseDir == "" (or a
			// template-resolution error) degrades to FullName().
			projectName, perr := config.ResolveComposeProjectName(baseDir, cfg)
			if perr != nil {
				projectName = cfg.Project.FullName()
			}
			name, err := daemon.ResolveContainerName(projectName, rendered)
			if err == nil {
				def2("resolved (with default params)", name, 4)
			}
		}
	}
}

// inspectParamsSection renders the Params section.
func inspectParamsSection(def2 inspectDef2, sub inspectSub, def *usercommands.CommandDef, translator i18n.Translator, locale string) {
	if len(def.Params) == 0 {
		return
	}
	sub("Params")
	var names []string
	for name := range def.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := def.Params[name]
		desc := string(p.Type)
		paramDesc := translator.ParamDescription(locale, def.ID, name, p.Description)
		if paramDesc != "" {
			desc = paramDesc + " (" + string(p.Type) + ")"
		}
		if p.Required {
			desc += " [required]"
		}
		if p.Default != "" {
			desc += " [default: " + p.Default + "]"
		}
		if p.DefaultFrom != "" {
			desc += " [default_from: " + p.DefaultFrom + "]"
		}
		def2(name, desc, 4)
	}
}

// inspectContextSection renders the Context section.
func inspectContextSection(def2 inspectDef2, sub inspectSub, def *usercommands.CommandDef) {
	if len(def.Context) == 0 {
		return
	}
	sub("Context")
	var names []string
	for name := range def.Context {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := def.Context[name]
		desc := "from: " + c.From
		if c.Required {
			desc += " [required]"
		}
		if c.Env != "" {
			desc += " [env: " + c.Env + "]"
		}
		def2(name, desc, 4)
	}
}

// inspectEnvSection renders the Env section.
func inspectEnvSection(def2 inspectDef2, sub inspectSub, def *usercommands.CommandDef) {
	if len(def.Env) == 0 {
		return
	}
	sub("Env")
	var keys []string
	for k := range def.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		def2(k, def.Env[k], 4)
	}
}

// inspectFilesSection renders the Files section.
func inspectFilesSection(def2 inspectDef2, sub inspectSub, def *usercommands.CommandDef) {
	if len(def.Files) == 0 {
		return
	}
	sub("Files")
	var fids []string
	for fid := range def.Files {
		fids = append(fids, fid)
	}
	sort.Strings(fids)
	for _, fid := range fids {
		f := def.Files[fid]
		desc := string(f.Access)
		if f.Path != "" {
			desc += "  path: " + f.Path
		} else if len(f.Candidates) > 0 {
			desc += fmt.Sprintf("  candidates: %d", len(f.Candidates))
		}
		if f.Env != "" {
			desc += "  env: " + f.Env
		}
		var flags []string
		if f.Required {
			flags = append(flags, "required")
		}
		if f.Mkdir {
			flags = append(flags, "mkdir")
		}
		if f.Overwrite {
			flags = append(flags, "overwrite")
		}
		if f.OnError != "" {
			flags = append(flags, "on_error: "+string(f.OnError))
		}
		if len(flags) > 0 {
			desc += "  [" + strings.Join(flags, ", ") + "]"
		}
		def2(fid, desc, 4)
	}
}
