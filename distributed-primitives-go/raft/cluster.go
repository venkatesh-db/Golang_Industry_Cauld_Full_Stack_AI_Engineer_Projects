package raft

import (
	"errors"
	"sync"
)

var (
	// ErrNoQuorum is returned when a candidate cannot reach a majority of
	// the whole cluster — the core safety property: no progress without a
	// majority, so two partitions can never both make decisions.
	ErrNoQuorum = errors.New("raft: no quorum — cannot elect a leader without a majority")
	// ErrFenced is returned when a stale leader (from an older term) tries
	// to act after a newer leader has been elected. This is what stops
	// split-brain double-writes once a partition heals.
	ErrFenced = errors.New("raft: leader is fenced — a newer term has taken over")
)

// Cluster is a fixed set of in-process nodes.
type Cluster struct {
	mu           sync.Mutex
	nodes        []*Node
	nextGroup    int
	committedTrm uint64 // highest term that has successfully led — the fence line
}

// NewCluster builds n followers, all initially in one group (fully
// connected).
func NewCluster(n int) *Cluster {
	c := &Cluster{nextGroup: 1}
	for i := 0; i < n; i++ {
		c.nodes = append(c.nodes, &Node{ID: i, State: Follower})
	}
	return c
}

// majority is ⌊N/2⌋+1 — a strict majority of the WHOLE cluster, not of
// whoever happens to be reachable. That is why a minority partition can
// never elect a leader.
func (c *Cluster) majority() int { return len(c.nodes)/2 + 1 }

// Partition isolates the given node ids into their own network group. The
// remaining nodes stay together. Simulates a network split.
func (c *Cluster) Partition(ids ...int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.nextGroup
	c.nextGroup++
	set := map[int]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for _, n := range c.nodes {
		if set[n.ID] {
			n.group = g
		}
	}
}

// Heal reconnects every node into a single group.
func (c *Cluster) Heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		n.group = 0
	}
}

// reachable counts how many nodes share the candidate's group — i.e. how
// many votes it can physically collect.
func (c *Cluster) reachable(candidate *Node) int {
	count := 0
	for _, n := range c.nodes {
		if n.group == candidate.group {
			count++
		}
	}
	return count
}

// Elect attempts to make node `id` the leader. It bumps the term above
// anything seen, then requires a majority of the whole cluster to be
// reachable. Succeeds only with quorum; otherwise ErrNoQuorum and the
// node steps back to Follower.
func (c *Cluster) Elect(id int) (*Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cand := c.nodes[id]
	cand.State = Candidate
	// New election term is one past the highest term in the cluster.
	var maxTerm uint64
	for _, n := range c.nodes {
		if n.Term > maxTerm {
			maxTerm = n.Term
		}
	}
	cand.Term = maxTerm + 1

	if c.reachable(cand) < c.majority() {
		cand.State = Follower
		return nil, ErrNoQuorum
	}

	// Won: demote any leader in the same group, promote candidate, and
	// have its group's followers adopt the new term.
	for _, n := range c.nodes {
		if n.group == cand.group && n.ID != cand.ID {
			n.State = Follower
			n.Term = cand.Term
		}
	}
	cand.State = Leader
	c.committedTrm = cand.Term
	return cand, nil
}

// NodeView is an immutable snapshot of a node's state — returned to
// callers (e.g. the UI) so they observe a value, not the live mutable
// *Node that later elections would change underneath them.
type NodeView struct {
	ID    int    `json:"id"`
	Term  uint64 `json:"term"`
	State string `json:"state"`
	Group int    `json:"group"`
}

// Snapshot returns a value view of every node plus the committed term (the
// fence line). Safe to call concurrently and safe to retain.
func (c *Cluster) Snapshot() (nodes []NodeView, committedTerm uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		nodes = append(nodes, NodeView{ID: n.ID, Term: n.Term, State: n.State.String(), Group: n.group})
	}
	return nodes, c.committedTrm
}

// Leaders returns every node currently believing it is leader. After a
// partition this can be >1 (split-brain belief) — but fencing in Write
// ensures only the newest term can actually commit.
func (c *Cluster) Leaders() []*Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*Node
	for _, n := range c.nodes {
		if n.State == Leader {
			out = append(out, n)
		}
	}
	return out
}
