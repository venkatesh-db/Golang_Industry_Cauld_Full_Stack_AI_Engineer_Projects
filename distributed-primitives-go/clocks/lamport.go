// Package clocks implements logical clocks: ways to order events across
// machines without trusting wall-clock time. Wall clocks on different
// servers drift and can even run backwards (NTP corrections), so an
// event with a "later" timestamp can actually have happened first —
// which quietly breaks any "last write wins" rule built on it.
package clocks

// Lamport is a scalar logical clock. It guarantees one direction of the
// happens-before relation: if event A causally precedes B, then
// L(A) < L(B). It does NOT guarantee the converse — L(A) < L(B) does not
// prove A caused B (they may be concurrent). That single limitation is
// exactly why vector clocks exist.
type Lamport struct {
	t uint64
}

// Tick is called on every local event. It advances the clock and returns
// the timestamp to stamp on that event.
func (l *Lamport) Tick() uint64 {
	l.t++
	return l.t
}

// Update is called when a message carrying timestamp `recv` arrives. The
// clock jumps ahead of anything it has seen, then ticks once for the
// receive event — this is what propagates causality across nodes.
func (l *Lamport) Update(recv uint64) uint64 {
	if recv > l.t {
		l.t = recv
	}
	l.t++
	return l.t
}

// Now returns the current value without advancing.
func (l *Lamport) Now() uint64 { return l.t }
