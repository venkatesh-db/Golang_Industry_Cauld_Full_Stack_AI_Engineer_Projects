package clocks

// HLC is a Hybrid Logical Clock. It solves a real operational problem:
// Lamport/vector timestamps are not human-meaningful (you can't sort logs
// by them and get wall-clock order), while raw wall clocks violate
// causality under skew. HLC returns timestamps that (a) stay within a
// bounded distance of physical time, so they read like real time, and
// (b) still respect happens-before, so causality holds even when the
// physical clock is stalled or jumps backward.
//
// It carries a physical component (max wall-clock seen) and a logical
// counter that only advances when physical time fails to.
type HLC struct {
	now      func() int64 // injectable wall clock (ms) — makes skew testable
	physical int64
	logical  uint64
}

// NewHLC takes a wall-clock source (inject a fake in tests to simulate
// skew or a frozen clock).
func NewHLC(now func() int64) *HLC {
	return &HLC{now: now}
}

// Now stamps a local event. If the physical clock advanced, the logical
// counter resets to 0; if it did not (stall or backward jump), the
// logical counter increments so timestamps still move strictly forward.
func (h *HLC) Now() (int64, uint64) {
	wall := h.now()
	if wall > h.physical {
		h.physical = wall
		h.logical = 0
	} else {
		h.logical++
	}
	return h.physical, h.logical
}

// Update merges a received (physical, logical) timestamp, keeping the
// clock ahead of anything observed while staying anchored to wall time.
func (h *HLC) Update(rp int64, rl uint64) (int64, uint64) {
	wall := h.now()
	max := h.physical
	if rp > max {
		max = rp
	}
	if wall > max {
		max = wall
	}
	switch {
	case max == h.physical && max == rp:
		if rl > h.logical {
			h.logical = rl
		}
		h.logical++
	case max == h.physical:
		h.logical++
	case max == rp:
		h.logical = rl + 1
	default:
		h.logical = 0
	}
	h.physical = max
	return h.physical, h.logical
}
