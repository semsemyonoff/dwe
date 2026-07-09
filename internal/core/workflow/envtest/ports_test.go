package envtest

import (
	"strings"
	"sync"
	"testing"
)

func TestAllocatePortsDistinct(t *testing.T) {
	t.Cleanup(resetLeases)
	ports, err := AllocatePorts(5)
	if err != nil {
		t.Fatalf("AllocatePorts: %v", err)
	}
	if len(ports) != 5 {
		t.Fatalf("len(ports) = %d, want 5", len(ports))
	}
	seen := make(map[int]bool, len(ports))
	for _, p := range ports {
		if p <= 0 {
			t.Errorf("port %d is not a valid positive port", p)
		}
		if seen[p] {
			t.Errorf("port %d allocated twice", p)
		}
		seen[p] = true
	}
}

func TestAllocatePortsZero(t *testing.T) {
	t.Cleanup(resetLeases)
	ports, err := AllocatePorts(0)
	if err != nil {
		t.Fatalf("AllocatePorts(0): %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("AllocatePorts(0) = %v, want empty", ports)
	}
}

func TestAllocatePortsNegative(t *testing.T) {
	t.Cleanup(resetLeases)
	ports, err := AllocatePorts(-1)
	if err != nil {
		t.Fatalf("AllocatePorts(-1): %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("AllocatePorts(-1) = %v, want empty", ports)
	}
}

// TestAllocatePortsSequentialDisjoint verifies that two sequential batches
// never return an overlapping port: the first batch's ports stay leased, so
// the second batch must harvest a fresh set.
func TestAllocatePortsSequentialDisjoint(t *testing.T) {
	t.Cleanup(resetLeases)
	first, err := AllocatePorts(4)
	if err != nil {
		t.Fatalf("AllocatePorts (first): %v", err)
	}
	second, err := AllocatePorts(4)
	if err != nil {
		t.Fatalf("AllocatePorts (second): %v", err)
	}
	firstSet := make(map[int]bool, len(first))
	for _, p := range first {
		firstSet[p] = true
	}
	for _, p := range second {
		if firstSet[p] {
			t.Errorf("port %d returned by both sequential batches", p)
		}
	}
}

// TestAllocatePortsConcurrentDisjoint drives many concurrent batches and
// asserts every returned port is unique across all of them. Run under -race to
// catch unsynchronized access to the lease set.
func TestAllocatePortsConcurrentDisjoint(t *testing.T) {
	t.Cleanup(resetLeases)
	const (
		batches      = 8
		portsPerCall = 4
	)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results [][]int
	)
	for range batches {
		wg.Go(func() {
			ports, err := AllocatePorts(portsPerCall)
			if err != nil {
				mu.Lock()
				t.Errorf("AllocatePorts: %v", err)
				mu.Unlock()
				return
			}
			mu.Lock()
			results = append(results, ports)
			mu.Unlock()
		})
	}
	wg.Wait()

	seen := make(map[int]bool, batches*portsPerCall)
	total := 0
	for _, ports := range results {
		for _, p := range ports {
			total++
			if seen[p] {
				t.Errorf("port %d returned by more than one concurrent batch", p)
			}
			seen[p] = true
		}
	}
	if want := batches * portsPerCall; total != want {
		t.Errorf("total ports = %d, want %d", total, want)
	}
}

// TestAllocatePortsExhaustion pre-leases every port so that every harvested
// port is already in the set, forcing AllocatePorts to exhaust its attempt
// budget and return the exhaustion error.
func TestAllocatePortsExhaustion(t *testing.T) {
	t.Cleanup(resetLeases)
	leaseMu.Lock()
	for p := 1; p <= 65535; p++ {
		leasedPorts[p] = struct{}{}
	}
	leaseMu.Unlock()

	_, err := AllocatePorts(1)
	if err == nil {
		t.Fatal("AllocatePorts: expected exhaustion error, got nil")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("error = %q, want it to mention exhaustion", err.Error())
	}
}
