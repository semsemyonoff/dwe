package envtest

import "testing"

func TestAllocatePortsDistinct(t *testing.T) {
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
	ports, err := AllocatePorts(0)
	if err != nil {
		t.Fatalf("AllocatePorts(0): %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("AllocatePorts(0) = %v, want empty", ports)
	}
}

func TestAllocatePortsNegative(t *testing.T) {
	ports, err := AllocatePorts(-1)
	if err != nil {
		t.Fatalf("AllocatePorts(-1): %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("AllocatePorts(-1) = %v, want empty", ports)
	}
}
