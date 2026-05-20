package liveui

// This file groups test-only accessors that expose LiveLine internals to
// tests in other packages (pipeline/, usercommands/runtime/). They are NOT
// part of the supported public API — production code must use the high-level
// methods only.

// IsPaused reports whether the LiveLine is currently paused. Test-only.
func (l *LiveLine) IsPaused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.paused
}

// IsStopped reports whether Stop has completed. Test-only.
func (l *LiveLine) IsStopped() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopped
}

// BlockRows returns the current number of reserved block rows (0 when not in
// block mode). Test-only.
func (l *LiveLine) BlockRows() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.blockRows
}

// BlockSlot returns the public snapshot for block row idx. Test-only.
type BlockSlot struct {
	Label     string
	Icon      string
	Finalized bool
}

// BlockSlotAt returns a snapshot of the block-row state at idx. Returns the
// zero value when idx is out of range. Test-only.
func (l *LiveLine) BlockSlotAt(idx int) BlockSlot {
	l.mu.Lock()
	defer l.mu.Unlock()
	if idx < 0 || idx >= len(l.blockSlots) {
		return BlockSlot{}
	}
	s := l.blockSlots[idx]
	return BlockSlot{Label: s.label, Icon: s.icon, Finalized: s.finalized}
}
