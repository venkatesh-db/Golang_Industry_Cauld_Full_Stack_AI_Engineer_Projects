package clocks

import "testing"

func TestLamportHappensBefore(t *testing.T) {
	// Node A does two local events, then sends to B.
	var a Lamport
	a.Tick()           // 1
	sendTS := a.Tick() // 2, piggybacked on the message

	var b Lamport
	b.Tick()                   // 1, an unrelated local event on B
	recvTS := b.Update(sendTS) // must exceed sendTS

	if recvTS <= sendTS {
		t.Fatalf("receive %d must be after send %d (happens-before broken)", recvTS, sendTS)
	}
}

func TestVectorDetectsConcurrency(t *testing.T) {
	a := NewVectorClock("a")
	b := NewVectorClock("b")
	a.Tick() // a:1
	b.Tick() // b:1 — concurrent, neither saw the other

	if got := Compare(a.Snapshot(), b.Snapshot()); got != Concurrent {
		t.Fatalf("independent writes must be Concurrent, got %s", got)
	}

	// Now b receives a's state → b causally follows a.
	b.Merge(a)
	if got := Compare(a.Snapshot(), b.Snapshot()); got != Before {
		t.Fatalf("after merge a must be Before b, got %s", got)
	}
}

func TestHLCMonotonicUnderFrozenClock(t *testing.T) {
	frozen := int64(1000)
	h := NewHLC(func() int64 { return frozen }) // wall clock never advances

	_, l0 := h.Now()
	_, l1 := h.Now()
	_, l2 := h.Now()
	// Physical is stuck, so the logical component must carry ordering.
	if !(l1 > l0 && l2 > l1) {
		t.Fatalf("logical counter must advance under a frozen clock: %d %d %d", l0, l1, l2)
	}
}

func TestHLCStaysNearWallClock(t *testing.T) {
	wall := int64(5000)
	h := NewHLC(func() int64 { return wall })
	p, _ := h.Now()
	if p != wall {
		t.Fatalf("HLC physical %d should track wall clock %d", p, wall)
	}
}
