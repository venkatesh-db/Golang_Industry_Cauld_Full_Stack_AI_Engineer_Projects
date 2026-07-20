// Package replication demonstrates the central problem of multi-leader
// (active-active) replication: two nodes accept writes independently, so
// the same key can be written concurrently and the system must reconcile
// the conflict. It contrasts two resolution strategies — Last-Writer-Wins
// (simple, lossy) and a CRDT (convergent, lossless) — so the trade-off is
// concrete rather than theoretical.
package replication

// LWWRegister resolves conflicts by timestamp: the write with the highest
// timestamp wins. This is what most "just use updated_at" designs reduce
// to. Its fatal flaw: it silently DISCARDS the losing write, and if the
// two clocks are skewed, the write that actually happened later can lose.
// LWW trades correctness for simplicity — acceptable for a user's last-
// selected theme, catastrophic for a bank balance.
type LWWRegister struct {
	Value string
	TS    int64  // wall-clock timestamp of the winning write
	Node  string // tie-breaker so merge is deterministic & commutative
}

// Set records a local write.
func (r *LWWRegister) Set(value string, ts int64, node string) {
	r.Value, r.TS, r.Node = value, ts, node
}

// Merge folds in another replica's state. Highest TS wins; ties break on
// node id so both replicas converge to the same value regardless of merge
// order. Returns true if this replica's value changed.
func (r *LWWRegister) Merge(other LWWRegister) bool {
	if other.TS > r.TS || (other.TS == r.TS && other.Node > r.Node) {
		*r = other
		return true
	}
	return false
}
