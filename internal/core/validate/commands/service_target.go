package commands

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// composeTargets is the set of compose service names a command's `service:`
// may name, loaded once per validator run and only when some command needs it.
//
// The runtime passes `service:` straight to `docker compose exec|run` (and a
// daemon's to `docker compose run`), so the target is a COMPOSE service name,
// not a dwe service key: a compose-only service defined inside a tool overlay
// is a valid target, and a dwe key whose `container:` differs is not.
type composeTargets struct {
	cfg    *config.DweConfig
	root   string
	loaded bool
	names  []string
	known  map[string]bool
	usable bool
}

func (t *composeTargets) load() {
	if t.loaded {
		return
	}
	t.loaded = true
	names, complete := config.ComposeServiceNames(t.cfg, t.root)
	if !complete {
		return
	}
	t.names = names
	t.known = make(map[string]bool, len(names))
	for _, n := range names {
		t.known[n] = true
	}
	t.usable = true
}

// serviceTargetDiagnostics warns when a service_exec / service_run / daemon
// command names a compose service that no overlay declares — a typo that
// otherwise only surfaces as a compose error at run time.
//
// Silent whenever absence cannot be proven: a templated value (resolved per
// invocation), no loaded config, or a compose chain that is not fully
// readable (config.compose_files reports missing files itself).
func serviceTargetDiagnostics(cmd model.CommandDef, relFile string, targets *composeTargets) []validate.Diagnostic {
	switch cmd.Type {
	case model.CommandTypeServiceExec, model.CommandTypeServiceRun, model.CommandTypeDaemon:
	default:
		return nil
	}
	svc := cmd.EffectiveService()
	if svc == "" || isTemplated(svc) || targets.cfg == nil {
		return nil
	}
	targets.load()
	if !targets.usable || targets.known[svc] {
		return nil
	}

	field := "service"
	if cmd.Runner != nil && cmd.Runner.Service != "" {
		field = "runner.service"
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityWarning,
		Domain:   "commands",
		Target:   fmt.Sprintf("commands:%s", cmd.ID),
		File:     relFile,
		Message: fmt.Sprintf("%s: %q is not a service in any compose overlay, disabled services included",
			field, svc),
		Hint: serviceTargetHint(svc, targets),
	}}
}

// serviceTargetHint names the likely fix: the compose name of a dwe service
// the author wrote by its key, else the closest compose service name.
func serviceTargetHint(svc string, targets *composeTargets) string {
	if dweSvc, ok := targets.cfg.Services[svc]; ok && dweSvc.Container != "" && targets.known[dweSvc.Container] {
		return fmt.Sprintf("dwe service %q runs as compose service %q — `service:` takes the compose name", svc, dweSvc.Container)
	}
	if alt := config.ClosestMatch(svc, targets.names); alt != "" {
		return fmt.Sprintf("did you mean %q?", alt)
	}
	return "known compose services: " + strings.Join(targets.names, ", ")
}

// isTemplated reports whether a service value is rendered per invocation
// (`${param.*}`, a Go template action), so its target is unknown until then.
func isTemplated(s string) bool {
	return strings.Contains(s, "${") || strings.Contains(s, "{{")
}
