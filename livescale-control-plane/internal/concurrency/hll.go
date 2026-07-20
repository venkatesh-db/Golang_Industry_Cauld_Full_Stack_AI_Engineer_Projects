package concurrency

import (
	"hash/maphash"
	"math"
	"math/bits"
	"sync/atomic"
)

// Estimator is a small HyperLogLog for approximate distinct-account counting
// (ADR-001). Reads and writes are lock-free: each register is updated with an
// atomic max, so Add never blocks the hot path. Accuracy target: ±2% (p=14).
//
// This is a deliberately compact internal implementation (no external dep) so
// the mechanism is fully explainable — registers are byte-max ranks, the
// estimate uses the standard harmonic-mean formula with small/large-range
// corrections.
type Estimator struct {
	regs []atomic.Uint32 // 4-byte cells; only low byte used, atomics need 32-bit
	m    float64
	p    uint
}

const hllP = 14 // 2^14 = 16384 registers -> ~0.81% standard error

// hllSeed is shared by every estimator so registers are comparable and Merge is
// valid across instances (and, at real scale, across nodes). A per-instance
// random seed would make merged registers meaningless.
var hllSeed = maphash.MakeSeed()

// NewEstimator builds an HLL estimator with 2^hllP registers.
func NewEstimator() *Estimator {
	m := uint32(1) << hllP
	return &Estimator{
		regs: make([]atomic.Uint32, m),
		m:    float64(m),
		p:    hllP,
	}
}

// Add folds a key into the estimator (lock-free).
func (e *Estimator) Add(key string) {
	h := maphash.String(hllSeed, key)
	idx := h >> (64 - e.p)             // top p bits pick the register
	w := (h << e.p) | (1 << (e.p - 1)) // remaining bits; guard against all-zero
	rank := uint32(bits.LeadingZeros64(w) + 1)
	cell := &e.regs[idx]
	for {
		cur := cell.Load()
		if rank <= cur {
			return
		}
		if cell.CompareAndSwap(cur, rank) {
			return
		}
	}
}

// Estimate returns the approximate cardinality.
func (e *Estimator) Estimate() float64 {
	sum := 0.0
	zeros := 0.0
	for i := range e.regs {
		r := e.regs[i].Load()
		sum += 1.0 / float64(uint64(1)<<r)
		if r == 0 {
			zeros++
		}
	}
	est := alpha(e.m) * e.m * e.m / sum
	// Small-range correction (linear counting) when many registers are empty.
	if est <= 2.5*e.m && zeros > 0 {
		est = e.m * math.Log(e.m/zeros)
	}
	return est
}

// Merge folds another estimator's registers into this one (register-wise max).
// Mergeability is why HLL is the right global choice at multi-node scale.
func (e *Estimator) Merge(o *Estimator) {
	for i := range e.regs {
		r := o.regs[i].Load()
		for {
			cur := e.regs[i].Load()
			if r <= cur || e.regs[i].CompareAndSwap(cur, r) {
				break
			}
		}
	}
}

func alpha(m float64) float64 {
	switch {
	case m >= 128:
		return 0.7213 / (1 + 1.079/m)
	case m >= 64:
		return 0.709
	case m >= 32:
		return 0.697
	default:
		return 0.5
	}
}
