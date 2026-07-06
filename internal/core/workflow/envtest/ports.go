package envtest

import (
	"fmt"
	"net"
)

// AllocatePorts harvests n free host ports by opening n ephemeral TCP
// listeners (net.Listen("tcp", ":0")) and keeping every one of them open
// until the whole batch has been claimed — only then are they all closed and
// the harvested port numbers returned. This guarantees intra-batch
// uniqueness (no two allocated ports collide with each other); it cannot
// guarantee the port stays free until the eventual docker bind (TOCTOU),
// which is accepted per spec §9 — the copy's own `ports_free` preflight check
// plus the runner's one-shot port-conflict retry are the safety net.
func AllocatePorts(n int) ([]int, error) {
	if n <= 0 {
		return nil, nil
	}
	listeners := make([]net.Listener, 0, n)
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	ports := make([]int, 0, n)
	for range n {
		l, err := net.Listen("tcp", ":0")
		if err != nil {
			return nil, fmt.Errorf("envtest: allocating free port: %w", err)
		}
		listeners = append(listeners, l)
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			return nil, fmt.Errorf("envtest: unexpected listener address type %T", l.Addr())
		}
		ports = append(ports, addr.Port)
	}
	return ports, nil
}
