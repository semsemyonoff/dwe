package linters

import (
	"sync/atomic"
	"testing"
	"time"

	"devbox-cli/internal/core/validate"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withMaxConcurrency swaps MaxLinterConcurrency for the duration of a test.
// Required for the barrier-based test below to succeed on 1-vCPU runners
// (default NumCPU=1 would force semaphore size 1 and deadlock the barrier).
func withMaxConcurrency(t *testing.T, n int) {
	t.Helper()
	prev := MaxLinterConcurrency
	MaxLinterConcurrency = n
	t.Cleanup(func() { MaxLinterConcurrency = prev })
}

// barrierValidator increments a shared started-counter and blocks on a barrier
// channel until released. Used to prove two children execute concurrently
// without timing assumptions.
type barrierValidator struct {
	id        string
	started   *atomic.Int32
	barrier   chan struct{}
	diag      validate.Diagnostic
	allInDone chan struct{}
	expected  int32
}

func (b *barrierValidator) ID() string     { return b.id }
func (b *barrierValidator) Domain() string { return Domain }
func (b *barrierValidator) Run(_ validate.Context) []validate.Diagnostic {
	n := b.started.Add(1)
	if n == b.expected {
		close(b.allInDone)
	}
	select {
	case <-b.barrier:
	case <-time.After(5 * time.Second):
		// safety bailout if the test is broken — surface as a failed diag
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   Domain,
			Target:   b.id,
			Message:  "barrier timed out",
		}}
	}
	return []validate.Diagnostic{b.diag}
}

func TestLintersGroup_RunGroup_RunsChildrenConcurrently(t *testing.T) {
	// Force concurrency to 2 so the test is deterministic regardless of NumCPU.
	withMaxConcurrency(t, 2)

	started := &atomic.Int32{}
	barrier := make(chan struct{})
	allIn := make(chan struct{})

	makeChild := func(id string) *barrierValidator {
		return &barrierValidator{
			id:        id,
			started:   started,
			barrier:   barrier,
			diag:      validate.Diagnostic{Severity: validate.SeverityOK, Domain: Domain, Target: id},
			allInDone: allIn,
			expected:  2,
		}
	}
	a := makeChild("a")
	b := makeChild("b")

	group := &lintersGroup{children: []validate.Validator{a, b}}

	// Wait for both children to enter Run() in a sibling goroutine, then release.
	go func() {
		select {
		case <-allIn:
		case <-time.After(2 * time.Second):
		}
		close(barrier)
	}()

	diags := group.RunGroup(validate.Context{}, group.children)
	assert.Len(t, diags, 2, "both children should have produced a diagnostic")
	assert.Equal(t, int32(2), started.Load(), "both children should have started")
}

func TestLintersGroup_RunGroup_PanicInOneChildDoesNotCancelSiblings(t *testing.T) {
	withMaxConcurrency(t, 2)

	good := &fakeChild{
		id:    "good",
		diags: []validate.Diagnostic{{Severity: validate.SeverityOK, Domain: Domain, Target: "good"}},
	}
	bad := &fakeChild{id: "bad", panicWith: "boom"}

	group := &lintersGroup{children: []validate.Validator{good, bad}}
	diags := group.RunGroup(validate.Context{}, group.children)

	// Sibling completed and produced its OK diagnostic.
	var sawGood, sawPanic bool
	for _, d := range diags {
		if d.Target == "good" && d.Severity == validate.SeverityOK {
			sawGood = true
		}
		if d.Target == "bad" && d.Severity == validate.SeverityError {
			sawPanic = true
		}
	}
	assert.True(t, sawGood, "good child should have produced its OK diagnostic")
	assert.True(t, sawPanic, "panicking child should surface as an Error diagnostic")
}

func TestLintersGroup_RunGroup_PreservesPerChildOrderAndIsRaceFree(t *testing.T) {
	withMaxConcurrency(t, 4)

	const n = 16
	kids := make([]validate.Validator, n)
	for i := range n {
		kids[i] = &fakeChild{
			id:    childID(i),
			diags: []validate.Diagnostic{{Severity: validate.SeverityOK, Domain: Domain, Target: childID(i)}},
		}
	}
	group := &lintersGroup{children: kids}
	diags := group.RunGroup(validate.Context{}, group.children)
	require.Len(t, diags, n)
	// Group output preserves input order (Registry will re-sort downstream).
	for i, d := range diags {
		assert.Equal(t, childID(i), d.Target)
	}
}

func TestLintersGroup_RunGroup_EmptyChildren_ReturnsNil(t *testing.T) {
	group := &lintersGroup{}
	diags := group.RunGroup(validate.Context{}, nil)
	assert.Nil(t, diags)
}

func TestLintersGroup_RunGroup_ZeroMaxConcurrency_StillRuns(t *testing.T) {
	// Guard against accidental zero/negative seam values deadlocking the group.
	withMaxConcurrency(t, 0)
	good := &fakeChild{
		id:    "good",
		diags: []validate.Diagnostic{{Severity: validate.SeverityOK, Domain: Domain, Target: "good"}},
	}
	group := &lintersGroup{children: []validate.Validator{good}}
	diags := group.RunGroup(validate.Context{}, group.children)
	assert.Len(t, diags, 1)
}

// fakeChild is a minimal validator with optional panic semantics.
type fakeChild struct {
	id        string
	diags     []validate.Diagnostic
	panicWith any
}

func (f *fakeChild) ID() string     { return f.id }
func (f *fakeChild) Domain() string { return Domain }
func (f *fakeChild) Run(_ validate.Context) []validate.Diagnostic {
	if f.panicWith != nil {
		panic(f.panicWith)
	}
	return f.diags
}

func childID(i int) string {
	return "c" + string(rune('a'+i))
}
