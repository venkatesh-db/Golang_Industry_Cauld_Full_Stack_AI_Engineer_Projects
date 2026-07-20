package replication

import "testing"

// TestLWWLosesDataUnderClockSkew is the headline failure interviewers
// probe: with skewed clocks, LWW keeps the write with the higher
// timestamp — which can be the one that actually happened FIRST — and
// silently drops the genuinely-later write.
func TestLWWLosesDataUnderClockSkew(t *testing.T) {
	// Real-world order: US writes "alice" first, then EU writes "bob".
	// But the US node's clock is skewed 10s fast, so its EARLIER write
	// carries the HIGHER timestamp.
	us := LWWRegister{Value: "alice", TS: 10_000, Node: "us"} // happened first, skewed-high ts
	eu := LWWRegister{Value: "bob", TS: 9_500, Node: "eu"}    // happened later, correct ts

	survivor := LWWOutcome(us, eu)

	if survivor.Value != "alice" {
		t.Fatalf("expected LWW to (wrongly) keep the skewed-high write, got %q", survivor.Value)
	}
	// Assert the data loss explicitly: "bob", the truly-later write, is gone.
	if survivor.Value == "bob" {
		t.Fatal("bob should have been lost — demonstrates LWW is unsafe under skew")
	}
}

// TestLWWConvergesRegardlessOfOrder — whatever LWW's flaw, it must at
// least be deterministic: both replicas reach the same value.
func TestLWWConvergesRegardlessOfOrder(t *testing.T) {
	a := LWWRegister{Value: "x", TS: 5, Node: "a"}
	b := LWWRegister{Value: "y", TS: 5, Node: "b"} // tie -> node id breaks it

	ab := LWWOutcome(a, b)
	ba := LWWOutcome(b, a)
	if ab.Value != ba.Value {
		t.Fatalf("merge not commutative: %q vs %q", ab.Value, ba.Value)
	}
}

// TestCRDTLosesNothing is the contrast: both sides' increments survive.
func TestCRDTLosesNothing(t *testing.T) {
	us := NewGCounter()
	eu := NewGCounter()
	us.Inc("us", 3) // 3 likes collected in US during the partition
	eu.Inc("eu", 4) // 4 likes collected in EU during the partition

	if total := CRDTOutcome(us, eu); total != 7 {
		t.Fatalf("CRDT must preserve all increments: want 7, got %d", total)
	}
}

// TestCRDTMergeIdempotent — re-merging duplicate state must not double-count.
func TestCRDTMergeIdempotent(t *testing.T) {
	g := NewGCounter()
	g.Inc("a", 5)
	dup := NewGCounter()
	dup.Merge(g)
	dup.Merge(g) // delivered twice, e.g. at-least-once replication
	if dup.Value() != 5 {
		t.Fatalf("idempotent merge expected 5, got %d", dup.Value())
	}
}
