package deploy_test

import (
	"devbox-cli/internal/config"
)

func makeDeployCfg(phases []config.DeployPhase) *config.DevboxConfig {
	return &config.DevboxConfig{
		Deploy: config.DeployConfig{Phases: phases},
		Raw:    map[string]any{"__configPath": "/tmp/devbox.yml"},
	}
}

func phaseWith(name string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", Steps: steps}
}

func phaseWithWhen(name, when string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", When: when, Steps: steps}
}

func cmdStep(name, cmd string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description"}
}

func commandStep(name, id string) config.DeployStep {
	return config.DeployStep{Name: name, Command: id, Description: name + " description"}
}

func commandStepWith(name, id string, with map[string]any) config.DeployStep {
	return config.DeployStep{Name: name, Command: id, With: with, Description: name + " description"}
}

func whenStep(name, cmd, when string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description", When: when}
}

func runtimeWhenStep(name, cmd, when string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description", When: when}
}

func checkStep(name, cmd, check string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description", Check: check}
}

func phaseWithUI(name, _ string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", Steps: steps}
}
