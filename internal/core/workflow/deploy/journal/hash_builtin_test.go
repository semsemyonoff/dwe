package journal_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

// TestBuiltinUpCheck_HashImpact pins both halves of the 0.6.1 upgrade note for
// the check: on the built-in up step. The project config hash covers check:, so
// every project on the built-in pipeline sees "config changed" once after the
// upgrade. The step hash does not, so up's own journal entry is not what
// triggers the re-run: the check is.
func TestBuiltinUpCheck_HashImpact(t *testing.T) {
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{"main": {Type: "app", Container: "main"}},
	}
	svcDeploys := map[string]*config.ServiceDeployConfig{"main": nil}
	tracked := []string{"main"}

	withCheck := deploy.DefaultDeployConfig()
	withoutCheck := deploy.DefaultDeployConfig()
	up := &withoutCheck.Phases[1].Steps[0]
	require.Equal(t, "up", up.Name)
	require.NotNil(t, up.Check, "the built-in up step must carry a check:")
	up.Check = nil

	assert.NotEqual(t,
		journal.ProjectConfigHash(cfg, withCheck, svcDeploys, tracked),
		journal.ProjectConfigHash(cfg, withoutCheck, svcDeploys, tracked),
		"check: must be part of the project config hash")
	assert.Equal(t,
		journal.StepHash(withCheck.Phases[1].Steps[0]),
		journal.StepHash(*up),
		"check: must not be part of the step hash")
}
