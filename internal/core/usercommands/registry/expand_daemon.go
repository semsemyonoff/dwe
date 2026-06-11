package registry

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
)

// expandDaemon turns a type=daemon CommandDef into up to four synthetic
// CommandDefs (.start/.logs/.stop/.restart). The source command is consumed
// by expansion — callers must NOT insert it into byID.
//
// Each synthetic is a regular CommandDef of type=builtin (or workflow for
// .restart). Param declarations on the source are copied to every synthetic
// so the param form renders correctly; runtime values flow through `with:`
// as ${param.<name>} template literals that renderBuiltinWith resolves at
// dispatch (registry expansion is config-blind — see invariant #14).
func expandDaemon(src model.CommandDef) []model.CommandDef {
	if src.Daemon == nil {
		return nil
	}

	base := src.ID

	// effectiveService/User/Workdir/WorkdirFrom mirror validateDaemonType's
	// resolution: runner.* overrides top-level fields when set.
	effectiveService := src.EffectiveService()
	effectiveUser := src.EffectiveUser()
	effectiveWorkdir := src.EffectiveWorkdir()
	effectiveWorkdirFrom := src.EffectiveWorkdirFrom()

	// label_params: keys are declared param names, values are template literals
	// rendered at runtime to the user's --set values. renderBuiltinWith walks
	// map[string]any so we must use that concrete type.
	labelParams := make(map[string]any, len(src.Params))
	for pname := range src.Params {
		labelParams[pname] = "${param." + pname + "}"
	}

	mk := func(local string) model.CommandDef {
		return model.CommandDef{
			Description:       src.Description,
			Private:           src.Private,
			Hide:              src.Hide,
			Bridge:            src.Bridge,
			Params:            src.Params,
			Context:           src.Context,
			Env:               src.Env,
			Group:             base,
			LocalName:         local,
			ID:                base + "." + local,
			SourceDaemon:      src.Daemon,
			DerivedFromDaemon: base,
		}
	}

	var out []model.CommandDef

	// Always generate all four virtual commands (start, logs, stop, restart)
	c := mk(model.DaemonControlStart)
	c.Type = model.CommandTypeBuiltin
	c.Cmd = "docker_daemon_start"
	with := map[string]any{
		"container_template": src.Daemon.ContainerTemplate,
		"daemon_id":          base,
		"label_params":       labelParams,
		"service":            effectiveService,
		"user":               string(effectiveUser),
		"workdir":            effectiveWorkdir,
		"workdir_from":       effectiveWorkdirFrom,
		"argv":               stringsToAnySlice(src.Argv),
		"compose_args":       stringsToAnySlice(src.ComposeArgs),
		"env":                stringMapToAnyMap(src.Env),
		"on_already_running": src.Daemon.OnAlreadyRunning,
	}
	if src.Daemon.AutoRemove != nil {
		with["auto_remove"] = *src.Daemon.AutoRemove
	}
	c.With = with
	out = append(out, c)

	c = mk(model.DaemonControlLogs)
	c.Type = model.CommandTypeBuiltin
	c.Cmd = "docker_daemon_logs"
	c.With = map[string]any{
		"container_template": src.Daemon.ContainerTemplate,
	}
	out = append(out, c)

	c = mk(model.DaemonControlStop)
	c.Type = model.CommandTypeBuiltin
	c.Cmd = "docker_daemon_stop"
	with = map[string]any{
		"container_template": src.Daemon.ContainerTemplate,
	}
	if src.Daemon.StopTimeout != "" {
		with["stop_timeout"] = src.Daemon.StopTimeout
	}
	c.With = with
	out = append(out, c)

	c = mk(model.DaemonControlRestart)
	c.Type = model.CommandTypeWorkflow
	passthrough := make(map[string]string, len(src.Params))
	for pname := range src.Params {
		passthrough[pname] = "${param." + pname + "}"
	}
	c.Steps = []model.WorkflowStep{
		{Command: base + "." + model.DaemonControlStop, With: passthrough},
		{Command: base + "." + model.DaemonControlStart, With: passthrough},
	}
	out = append(out, c)

	return out
}

// expandAndInsertDaemon expands a source daemon CommandDef into synthetics and
// inserts each into the registry under a new group node named after the daemon
// base ID. The source itself is never inserted — `reg.Get("<base>")` returns
// not-found by design.
func (r *Registry) expandAndInsertDaemon(src model.CommandDef) error {
	base := src.ID
	baseGN := r.ensureGroup(base)
	if baseGN.Meta.Title == "" && baseGN.Meta.Description == "" {
		baseGN.Meta = model.GroupMeta{
			Title:       src.LocalName,
			Description: src.Description,
		}
	}
	// Propagate src.Hide onto the synthetic group's Meta.Hide so that
	// ApplyVisibility cascades to all 4 synthetics from one evaluation
	// instead of running the same expression 4×. Also ensures the synthetic
	// group node itself is Hidden (no phantom group header under
	// `dwe commands list --all`).
	if src.Hide != "" && baseGN.Meta.Hide == "" {
		baseGN.Meta.Hide = src.Hide
	}

	synthetics := expandDaemon(src)
	for i := range synthetics {
		s := synthetics[i]
		if existing, dup := r.byID[s.ID]; dup {
			return fmt.Errorf(
				"command %q auto-generated from daemon %q collides with explicit command (group %s)",
				s.ID, base, existing.Group)
		}
		r.byID[s.ID] = &s
		baseGN.Commands = append(baseGN.Commands, &s)
	}
	return nil
}

func stringsToAnySlice(in []string) []any {
	if in == nil {
		return nil
	}
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func stringMapToAnyMap(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
