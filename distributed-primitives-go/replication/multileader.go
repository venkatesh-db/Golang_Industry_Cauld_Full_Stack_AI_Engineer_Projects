package replication

// This file stages a head-to-head conflict so the LWW-vs-CRDT trade-off
// is observable, not just described.
//
// Scenario: two leaders (say a US region and an EU region) each accept a
// write to the same logical value while the link between them is down.
// When the link heals they exchange state and must reconcile.

// LWWOutcome merges two concurrent LWW writes and reports the survivor.
// The point it makes: one of the two real user writes is thrown away.
func LWWOutcome(a, b LWWRegister) LWWRegister {
	winner := a
	winner.Merge(b)
	return winner
}

// CRDTOutcome merges two G-Counters that each recorded local increments
// during the partition. The point it makes: NO increment is lost — the
// total is the true sum of both sides' work.
func CRDTOutcome(a, b *GCounter) uint64 {
	merged := NewGCounter()
	merged.Merge(a)
	merged.Merge(b)
	return merged.Value()
}
