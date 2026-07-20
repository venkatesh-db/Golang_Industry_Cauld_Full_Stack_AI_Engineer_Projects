package replication

// GCounter is a grow-only counter CRDT (Conflict-free Replicated Data
// Type). Each node increments only its OWN slot; the merged value is the
// sum of every node's slot. Because merge is element-wise max and the
// slots never collide, concurrent increments are never lost — the counter
// converges to the correct total no matter the order or duplication of
// merges. This is the lossless alternative to LWW: it needs no clocks and
// no coordination, at the cost of only supporting operations that
// commute (here, increment).
type GCounter struct {
	counts map[string]uint64
}

func NewGCounter() *GCounter {
	return &GCounter{counts: map[string]uint64{}}
}

// Inc increments this node's slot by n.
func (g *GCounter) Inc(node string, n uint64) {
	g.counts[node] += n
}

// Value is the total across all nodes.
func (g *GCounter) Value() uint64 {
	var sum uint64
	for _, c := range g.counts {
		sum += c
	}
	return sum
}

// Merge takes the per-node max — idempotent, commutative, associative, so
// replicas always converge regardless of merge order or repeats.
func (g *GCounter) Merge(other *GCounter) {
	for node, c := range other.counts {
		if c > g.counts[node] {
			g.counts[node] = c
		}
	}
}
