package raft

import (
	"sync"
	"testing"
)

// TestElectsWithQuorum — a healthy fully-connected cluster elects exactly
// one leader.
func TestElectsWithQuorum(t *testing.T) {
	c := NewCluster(5)
	leader, err := c.Elect(0)
	if err != nil {
		t.Fatalf("healthy cluster should elect: %v", err)
	}
	if leader.State != Leader {
		t.Fatalf("node 0 should be Leader, got %s", leader.State)
	}
}

// TestNoProgressWithoutQuorum — the minority side of a partition CANNOT
// elect. This is the safety property that makes consensus safe: a
// minority never makes progress, so two partitions can't diverge.
func TestNoProgressWithoutQuorum(t *testing.T) {
	c := NewCluster(5)
	// Split into {0,1} (minority) and {2,3,4} (majority).
	c.Partition(0, 1)

	if _, err := c.Elect(0); err != ErrNoQuorum {
		t.Fatalf("minority (2 of 5) must fail to elect, got %v", err)
	}
	// The majority side elects fine.
	if _, err := c.Elect(2); err != nil {
		t.Fatalf("majority (3 of 5) must elect, got %v", err)
	}
}

// TestFencingPreventsSplitBrain — the canonical incident. Old leader is
// partitioned, majority elects a newer leader, old leader returns and its
// stale-term write is fenced off.
func TestFencingPreventsSplitBrain(t *testing.T) {
	c := NewCluster(5)
	oldLeader, _ := c.Elect(0) // term 1

	// Old leader (node 0) is isolated; majority {1,2,3,4} elects a new one.
	c.Partition(0)
	newLeader, err := c.Elect(1) // term 2, higher
	if err != nil {
		t.Fatalf("majority should elect new leader: %v", err)
	}
	if newLeader.Term <= oldLeader.Term {
		t.Fatalf("new term %d must exceed old %d", newLeader.Term, oldLeader.Term)
	}

	c.Heal() // node 0 comes back still thinking it is leader

	applied := false
	// Stale leader's write must be fenced.
	if err := c.Write(oldLeader.ID, func() { applied = true }); err != ErrFenced {
		t.Fatalf("stale leader write must be fenced, got %v", err)
	}
	if applied {
		t.Fatal("stale write leaked through fencing — split-brain corruption")
	}
	// New leader's write succeeds.
	if err := c.Write(newLeader.ID, func() { applied = true }); err != nil {
		t.Fatalf("current leader write should succeed: %v", err)
	}
	if !applied {
		t.Fatal("current leader write did not apply")
	}
}

// TestConcurrentElectionsRaceClean — hammer Elect from many goroutines to
// prove the cluster is safe under -race; the mutex is the single owner of
// cluster state (same single-owner discipline as the ledger shards).
func TestConcurrentElectionsRaceClean(t *testing.T) {
	c := NewCluster(5)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, _ = c.Elect(id)
		}(i)
	}
	wg.Wait()
	// Exactly one term should have committed as the highest; at most one
	// leader can hold the committed term.
	committed := 0
	for _, l := range c.Leaders() {
		if l.Term == c.committedTrm {
			committed++
		}
	}
	if committed > 1 {
		t.Fatalf("more than one leader at the committed term: %d", committed)
	}
}
