// Package raft is a teaching-grade, in-process model of the parts of a
// consensus protocol that interviews actually probe: leader election,
// why a majority quorum is required, and how fencing prevents split-brain
// after a partition heals. It deliberately omits log replication RPC,
// disk persistence, and real timers — a network is simulated by grouping
// nodes so the *logic* is deterministic and race-testable, not the socket
// plumbing.
package raft

// State is a node's role in a term.
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	default:
		return "Leader"
	}
}

// Node is one member of the cluster. `group` simulates network
// reachability: two nodes can exchange messages only if they share a
// group. A partition splits the cluster into groups with different ids.
type Node struct {
	ID    int
	Term  uint64
	State State
	group int
}
