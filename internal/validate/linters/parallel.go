package linters

import (
	"fmt"
	"runtime"
	"sync"

	"devbox-cli/internal/validate"
)

// MaxLinterConcurrency caps how many child linters in lintersGroup.RunGroup
// run concurrently. Linters are subprocess-bound, but capping at NumCPU keeps
// the host responsive. Exported so tests can swap it (e.g. force 2 for the
// barrier-based concurrency proof on a 1-vCPU runner).
var MaxLinterConcurrency = runtime.NumCPU()

// lintersGroup wraps the per-linter validators produced by All() into a single
// GroupValidator. Registry.Run expands children for scope matching and calls
// RunGroup so the group can fan-out execution. The group's own ID/Domain are
// housekeeping only — children carry the scope-visible IDs.
type lintersGroup struct {
	children []validate.Validator
}

func (g *lintersGroup) ID() string                                   { return Domain }
func (g *lintersGroup) Domain() string                               { return Domain }
func (g *lintersGroup) Run(_ validate.Context) []validate.Diagnostic { return nil }
func (g *lintersGroup) Children() []validate.Validator               { return g.children }

// RunGroup fans out the matching children concurrently and concatenates their
// diagnostics in deterministic (input) order. Registry re-sorts downstream, so
// the input order here is a stability detail rather than a user-visible one —
// keeping it deterministic helps tests assert exact slices.
//
// Goroutine contract (CRITICAL):
//   - sync.WaitGroup (NOT errgroup.WithContext) so one linter's failure cannot
//     cancel siblings. Per-linter cancellation already comes from the
//     context.WithTimeout in runtime.go.
//   - each goroutine wraps work in a deferred recover(): a panic in one child
//     becomes a single Error diagnostic for that child, and siblings still run
//     to completion.
//   - each goroutine writes only to its own pre-allocated results[i] slot. No
//     shared slice, no mutex.
func (g *lintersGroup) RunGroup(vctx validate.Context, children []validate.Validator) []validate.Diagnostic {
	if len(children) == 0 {
		return nil
	}

	limit := min(max(MaxLinterConcurrency, 1), len(children))
	sem := make(chan struct{}, limit)

	results := make([][]validate.Diagnostic, len(children))
	var wg sync.WaitGroup
	for i, child := range children {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, child validate.Validator) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					results[i] = []validate.Diagnostic{{
						Severity: validate.SeverityError,
						Domain:   Domain,
						Target:   child.ID(),
						Message:  fmt.Sprintf("%s: panic: %v", child.ID(), r),
					}}
				}
			}()
			results[i] = child.Run(vctx)
		}(i, child)
	}
	wg.Wait()

	var out []validate.Diagnostic
	for _, r := range results {
		out = append(out, r...)
	}
	return out
}
