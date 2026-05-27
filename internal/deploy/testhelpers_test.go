package deploy_test

import (
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
)

func makeDeployCfg(phases []config.DeployPhase) *config.DevboxConfig {
	return &config.DevboxConfig{
		Deploy: &config.ProjectDeployConfig{Phases: phases},
		Raw:    map[string]any{"__configPath": "/tmp/devbox.yml"},
	}
}

func phaseWith(name string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", Steps: steps}
}

func phaseWithWhen(name, when string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", When: parseWhenString(when), Steps: steps}
}

func cmdStep(name, cmd string) config.DeployStep {
	return config.DeployStep{Name: name, Type: "shell", Cmd: cmd, Description: name + " description"}
}

func commandStep(name, id string) config.DeployStep {
	return config.DeployStep{Name: name, Type: "command", Cmd: id, Description: name + " description"}
}

func commandStepWith(name, id string, with map[string]any) config.DeployStep {
	return config.DeployStep{Name: name, Type: "command", Cmd: id, With: with, Description: name + " description"}
}

func whenStep(name, cmd, when string) config.DeployStep {
	return config.DeployStep{Name: name, Type: "shell", Cmd: cmd, Description: name + " description", When: parseWhenString(when)}
}

func runtimeWhenStep(name, cmd, when string) config.DeployStep {
	return config.DeployStep{Name: name, Type: "shell", Cmd: cmd, Description: name + " description", When: parseWhenString(when)}
}

// parseWhenString converts a legacy when string to a typed condition.
// Supports:
// - "{{...}}" → template
// - "cmd: ..." → shell
// - "dir-empty ..." → builtin
func parseWhenString(when string) *condition.Condition {
	if when == "" {
		return nil
	}
	kind, payload := condition.Classify(when)
	switch kind {
	case condition.KindTemplate:
		return &condition.Condition{Type: condition.TypeTemplate, Expr: payload}
	case condition.KindBuiltin:
		return &condition.Condition{Type: condition.TypeBuiltin, Cmd: payload}
	case condition.KindCmd:
		return &condition.Condition{Type: condition.TypeShell, Cmd: payload}
	default:
		return nil
	}
}

func checkStep(name, cmd, check string) config.DeployStep {
	// Parse the check string as a builtin predicate or shell command
	checkType := "shell"
	kind, payload := condition.Classify(check)
	switch kind {
	case condition.KindBuiltin:
		checkType = "builtin"
		check = payload
	case condition.KindCmd:
		checkType = "shell"
		check = payload
	}
	return config.DeployStep{Name: name, Type: "shell", Cmd: cmd, Description: name + " description", Check: &config.Action{Type: checkType, Cmd: check}}
}

func phaseWithUI(name, _ string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", Steps: steps}
}
