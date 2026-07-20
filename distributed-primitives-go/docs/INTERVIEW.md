# distributed-primitives-go — 3-Minute Interview Walkthrough

> Run everything: `go test -race ./...` (12 pass) · `go run ./cmd/demo`

This repo exists to answer the distributed-systems questions a single app never exercises: **consensus, multi-leader conflict resolution, and logical time.** It's an in-process simulation on purpose — a network is faked by grouping nodes so the *logic* is deterministic and race-testable, not the socket plumbing. That framing is itself a good interview answer: "I modeled the parts under test and stubbed the transport."

## 1. Consensus — `raft/`
**Say:** "Five nodes. A leader is elected by winning a **majority of the whole cluster** — not a majority of who's reachable. That's the safety property: a minority partition can never elect, so two sides can't both make progress."

- `cluster.go` → `Elect(id)` bumps the term above anything seen, then requires `⌊N/2⌋+1` reachable. Minority → `ErrNoQuorum`.
- `fencing.go` → `Write` rejects any leader whose term is below the highest committed term. **This is how split-brain is prevented**: an old leader that was partitioned away comes back, still believing it leads, and its stale-term write is fenced. The monotonic term *is* the fencing token — same idea as a lock generation number in a distributed lock.
- Proof: `TestFencingPreventsSplitBrain`, `TestNoProgressWithoutQuorum`.

**Follow-up you can now field:** "Why can't consensus make progress without a majority?" → two minorities could each elect and diverge; a majority guarantees any two quorums overlap in at least one node.

## 2. Multi-leader replication — `replication/`
**Say:** "Two leaders accept writes independently during a partition, so the same key gets concurrent writes. You must reconcile. I show two strategies side by side."

- `lww.go` → Last-Writer-Wins by timestamp. Simple, and what `updated_at` designs reduce to. **Its flaw is the headline:** under clock skew the write that happened *later* can carry the *lower* timestamp and get silently dropped. `TestLWWLosesDataUnderClockSkew` asserts exactly that data loss.
- `gcounter.go` → a CRDT. Each node increments only its own slot; merge is per-node max. Concurrent increments are **never lost**, merge is idempotent (safe under at-least-once delivery). `TestCRDTLosesNothing`, `TestCRDTMergeIdempotent`.

**The trade-off in one line:** LWW buys simplicity by discarding data; CRDTs stay lossless but only support operations that commute.

## 3. Logical clocks — `clocks/`
**Say:** "You can't trust wall clocks across servers — they drift and jump backward on NTP correction — so I order events logically."

- `lamport.go` → scalar clock; guarantees `A→B ⇒ L(A)<L(B)`, but the converse doesn't hold, which is *why* vector clocks exist.
- `vector.go` → detects genuine concurrency (`Concurrent` vs `Before`/`After`). That's the extra bit Lamport can't give you.
- `hlc.go` → Hybrid Logical Clock: timestamps that read like wall-clock time **and** respect causality; stays monotonic even when the physical clock is frozen (`TestHLCMonotonicUnderFrozenClock`).

## How this connects to the other repos
- **`fintech-ledger-go`** uses single-owner shards — it *assumes* one writer per account. This repo shows how you'd actually *elect* that owner (raft) and what happens when ownership is contested (fencing).
- **`fintech-gateway-go`** has a consistent-hash ring for routing; this repo is the coordination layer beneath it.

Together the three repos tell one story: **correctness (ledger) · resilience at the edge (gateway) · coordination & time (this).**
