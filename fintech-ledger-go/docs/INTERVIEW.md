# fintech-ledger-go — 3-Minute Interview Walkthrough

> Run: `go test ./...` (9 packages green) · demo: `go run ./cmd/demo`

**One-liner:** "A wallet/ledger core in Go. Its whole reason for existing is *correctness under money and concurrency* — exact arithmetic, single-owner state, retry-safe writes, and cross-system moves done as sagas, not distributed transactions."

## The 90-second architecture story
Money enters via a transfer/withdraw request → the amount is exact integer paise → the mutation is serialized by the account's single owner → external legs (bank payout) run as a **saga** with compensations → a front-of-ledger **cache** serves fast reads and is *allowed to lag* → an evening **reconciliation** batch matches our log against the bank statement.

## Point-at-the-line moments

**1. Money is `int64`, never float — `internal/money/money.go`**
"float64 can't represent 0.1 exactly; sum millions of paise and the rounding error compounds. Paise is an int64 count of the smallest unit, so every op is exact." Answers: *why not float for money.*

**2. Single-owner state — `internal/partition/shard.go` + `ledger/account.go`**
"Each account has exactly one owner — a goroutine serializing every command for its shard. A shared map touched by every request goroutine has no single owner, which is what lets two nodes process the same transaction concurrently." Answers: *sharding, shard keys, hotspots, state ownership, how you avoid races.* Account-level mutation is also mutex-guarded; `Transfer` deliberately takes two locks in a fixed order.

**3. Idempotency is DB-enforced, not best-effort — `internal/idempotency/store.go`**
"The interface is `Reserve/Complete/Release`. In production it's Postgres `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING` — a unique constraint enforced by the database, not an in-process check. `Reserve` returns `ErrInProgress` for an in-flight duplicate." Answers: *idempotency keys, why retries are dangerous, how an idempotency key works.* This is the fix for the **double-charge incident** (see the Answer Map) — the bug was reading key state from a lagging replica; the fix moves it to the primary.

**4. Saga, not 2PC — `internal/saga/withdraw.go`**
"Withdraw-to-bank is a saga: debit the wallet locally (account locked only for that step), then payout; if payout fails, compensate with a credit. 2PC would hold the wallet locked for the whole bank round-trip, and the bank won't join our coordinator anyway." It even handles the nasty case where the *compensation itself* fails. Answers: *ACID across services, 2PC weakness, saga, compensating transactions.*

**5. Cache that's allowed to lag — `internal/wallet/cache.go`**
"Front-of-ledger balance cache, mutex-guarded (a bare map mutated by many goroutines is a crash-level data race). It *intentionally* lags — anyone needing the authoritative just-written balance reads `ledger.Account` directly." Answers: *cache-aside, staleness, read-your-writes, when eventual consistency is fine.*

**6. Bounded worker pool — `internal/reconciliation/worker.go`**
"Evening reconciliation runs txns through a fixed pool of N workers that exit when the jobs channel closes, honoring ctx cancellation. One-goroutine-per-txn with no exit path leaks goroutines until the process OOMs by evening." Answers: *backpressure, goroutine leaks, worker pools, load spikes.*

## Follow-ups you can now field
- *"How do you keep cache and DB consistent?"* → invalidate-on-write + authoritative reads bypass cache; the cache is a latency optimization, not a source of truth.
- *"What broke in prod once?"* → the double-charge-under-retry story, rooted in a replica-lag read of the idempotency key.
