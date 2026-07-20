package raft

// Write applies a value on behalf of a node claiming leadership. The
// fencing check is the whole point: a write is accepted only if the
// writer's term is at least the highest term that has led (committedTrm).
//
// Consider the classic split-brain: an old leader is partitioned away, a
// new leader is elected on the majority side with a higher term, then the
// old leader comes back still believing it is leader. Without fencing it
// would happily accept writes and corrupt state. With fencing its stale
// (lower) term is rejected — the monotonic term acts as the fencing token
// exactly like a lock generation number in a distributed lock.
func (c *Cluster) Write(leaderID int, apply func()) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	n := c.nodes[leaderID]
	if n.State != Leader || n.Term < c.committedTrm {
		return ErrFenced
	}
	apply()
	return nil
}
