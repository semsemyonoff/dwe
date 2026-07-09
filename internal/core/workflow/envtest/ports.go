package envtest

import (
	"fmt"
	"net"
	"sync"
)

// leaseMu guards leasedPorts. Both are package-level so that every
// AllocatePorts call in this process shares one lease set.
var (
	leaseMu     sync.Mutex
	leasedPorts = map[int]struct{}{}
)

// AllocatePorts harvests n free host ports by opening n ephemeral TCP
// listeners (net.Listen("tcp", ":0")) and keeping every one of them open
// until the whole batch has been claimed — only then are they all closed and
// the harvested port numbers returned. This guarantees intra-batch
// uniqueness (no two allocated ports collide with each other); it cannot
// guarantee the port stays free until the eventual docker bind (TOCTOU),
// which is accepted per spec §9 — the copy's own `ports_free` preflight check
// plus the runner's one-shot port-conflict retry are the safety net.
//
// To also guarantee INTER-batch uniqueness within this process (two batches
// running concurrently, e.g. under `--parallel N`, must never return the same
// port), AllocatePorts consults a process-wide lease set: a harvested port
// already leased by an earlier batch is skipped (its listener closed, a fresh
// one opened) and every returned port is registered. Because a batch holds its
// listener open until the port is registered, no two batches can hold the same
// OS port simultaneously, so the check-and-register under leaseMu is race-safe.
// Leases are NEVER released: `dwe test` is a short-lived process that allocates
// at most dozens of ports, so a monotonically growing set is fine and avoids
// the reuse window a release would open. A bounded per-batch attempt count
// guards against a pathological loop where the OS keeps handing back leased
// ports; exhaustion returns an error.
func AllocatePorts(n int) ([]int, error) {
	if n <= 0 {
		return nil, nil
	}
	maxAttempts := 100 + 20*n
	listeners := make([]net.Listener, 0, n)
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	ports := make([]int, 0, n)
	attempts := 0
	for len(ports) < n {
		if attempts >= maxAttempts {
			return nil, fmt.Errorf("envtest: allocating free port: exhausted %d attempts (%d ports leased)", maxAttempts, len(leasedPorts))
		}
		attempts++
		l, err := net.Listen("tcp", ":0")
		if err != nil {
			return nil, fmt.Errorf("envtest: allocating free port: %w", err)
		}
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			_ = l.Close()
			return nil, fmt.Errorf("envtest: unexpected listener address type %T", l.Addr())
		}
		port := addr.Port

		leaseMu.Lock()
		if _, leased := leasedPorts[port]; leased {
			leaseMu.Unlock()
			// Port belongs to another batch's lease; drop this listener and
			// retry so the OS hands us a different one.
			_ = l.Close()
			continue
		}
		leasedPorts[port] = struct{}{}
		leaseMu.Unlock()

		listeners = append(listeners, l)
		ports = append(ports, port)
	}
	return ports, nil
}

// resetLeases clears the process-wide lease set. Test-only: production never
// releases leases (see AllocatePorts). Tests register it via
// t.Cleanup(resetLeases) so the global does not leak between sibling tests.
func resetLeases() {
	leaseMu.Lock()
	defer leaseMu.Unlock()
	leasedPorts = map[int]struct{}{}
}
