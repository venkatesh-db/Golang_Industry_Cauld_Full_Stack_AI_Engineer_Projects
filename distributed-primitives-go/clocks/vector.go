package clocks

// Ordering is the result of comparing two vector clocks.
type Ordering int

const (
	Equal      Ordering = iota // identical vectors
	Before                     // a happened-before b
	After                      // b happened-before a
	Concurrent                 // neither precedes the other — a real concurrent write
)

func (o Ordering) String() string {
	switch o {
	case Equal:
		return "Equal"
	case Before:
		return "Before"
	case After:
		return "After"
	default:
		return "Concurrent"
	}
}

// VectorClock tracks one counter per node. Unlike a Lamport clock it can
// prove concurrency: if neither vector dominates the other, the two
// events are genuinely concurrent and a conflict-resolution policy (LWW,
// CRDT, or ask-the-user) must decide — you cannot just pick the "larger"
// timestamp. This is the extra information vector clocks buy over Lamport.
type VectorClock struct {
	node string
	vec  map[string]uint64
}

func NewVectorClock(node string) *VectorClock {
	return &VectorClock{node: node, vec: map[string]uint64{}}
}

// Tick advances this node's own component for a local event.
func (v *VectorClock) Tick() {
	v.vec[v.node]++
}

// Merge folds in a received vector (element-wise max) then ticks locally,
// recording "I observed everything the sender had, plus this receive."
func (v *VectorClock) Merge(other *VectorClock) {
	for node, c := range other.vec {
		if c > v.vec[node] {
			v.vec[node] = c
		}
	}
	v.Tick()
}

// Snapshot returns a copy of the raw counters (for comparison/transport).
func (v *VectorClock) Snapshot() map[string]uint64 {
	out := make(map[string]uint64, len(v.vec))
	for k, c := range v.vec {
		out[k] = c
	}
	return out
}

// Compare reports how two clock snapshots relate. It walks the union of
// keys so a node absent from one vector counts as zero there.
func Compare(a, b map[string]uint64) Ordering {
	var aGreater, bGreater bool
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	for k := range seen {
		switch {
		case a[k] > b[k]:
			aGreater = true
		case a[k] < b[k]:
			bGreater = true
		}
	}
	switch {
	case aGreater && bGreater:
		return Concurrent
	case aGreater:
		return After
	case bGreater:
		return Before
	default:
		return Equal
	}
}
