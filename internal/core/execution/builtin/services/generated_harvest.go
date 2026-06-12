package services

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	configpack "github.com/semsemyonoff/dwe/internal/core/execution/templates/config"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
)

// harvestMu serializes the load → harvest → save critical section on the
// generated-value store. Each invocation reads, mutates only its own service's
// keys, and rewrites the whole store; without serialization two harvest steps
// for different services placed in the same parallel: group would race and the
// last writer would silently drop the other service's just-harvested secret.
// Cross-process concurrency is already excluded by the project deploy lock, so a
// process-level mutex closes the remaining within-process window. Loading inside
// the lock guarantees the second harvester observes the first's committed write
// and merges rather than clobbers.
var harvestMu sync.Mutex

// GeneratedHarvest implements the service_generated_harvest builtin: read each of
// the service's declared generated: fields from its on-disk file, extract the
// value via the field's regex (capture group 1), and write-if-absent it into the
// generated-value store (.dwe/generated.yml).
//
// "Harvest, not mint": the service's own generator (e.g. php artisan
// key:generate) writes the secret; DWE only reads it back and replays it on
// later renders. Write-if-absent means a value already in the store is preserved,
// so a redeploy is a no-op. A service with no generated: fields is a no-op.
type GeneratedHarvest struct{}

// Validate checks the with-params for service_generated_harvest.
func (GeneratedHarvest) Validate(with map[string]any) error {
	service := spec.GetStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_generated_harvest: missing required param 'service'")
	}
	return nil
}

// Describe returns a human-readable plan line for service_generated_harvest.
func (GeneratedHarvest) Describe(with map[string]any) string {
	service := spec.GetStringParam(with, "service", "")
	return fmt.Sprintf("builtin: service_generated_harvest(service=%s)", service)
}

// Run harvests the service's declared generated values into the store.
func (GeneratedHarvest) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	serviceName := spec.GetStringParam(with, "service", "")

	// Serialize the load → harvest → save sequence so concurrent harvest steps
	// (parallel: groups) merge into the store instead of clobbering each other.
	harvestMu.Lock()
	defer harvestMu.Unlock()

	storePath := filepath.Join(ectx.ProjectRoot, generatedstore.DefaultRelPath)
	store, err := generatedstore.Load(storePath)
	if err != nil {
		return fmt.Errorf("service_generated_harvest: load generated store: %w", err)
	}

	// HarvestGenerated saves the store atomically when it writes a new value.
	res, err := configpack.HarvestGenerated(ectx.ProjectRoot, ectx.Config, serviceName, store)
	if err != nil {
		return fmt.Errorf("service_generated_harvest: %w", err)
	}
	if ectx.Output != nil {
		for _, f := range res.Fields {
			if f.Wrote {
				ectx.Output.Success(fmt.Sprintf("harvested %s.%s [stored]", serviceName, f.Field))
			} else {
				ectx.Output.Info(fmt.Sprintf("harvested %s.%s [already present, kept]", serviceName, f.Field))
			}
		}
	}
	return nil
}
