# Design decisions

Each entry: the gap, why it's a real Indian-fintech failure mode, and the
fix as it exists in this repo.

## No idempotency key on payment APIs

**Gap:** client retries after a timeout double-debit. This is the #1
recurring incident class in payment systems — a mobile client on a flaky
network sends a debit, the response never arrives, the client (correctly,
from its own point of view) retries.

**Fix:** [`internal/payment/debit.go`](../internal/payment/debit.go) requires
an `Idempotency-Key` and reserves it via
[`internal/idempotency/store.go`](../internal/idempotency/store.go) before
touching the ledger. A replayed key returns the stored result instead of
re-running the debit. See
[`TestDebit_RetryWithSameKeyDoesNotDoubleDebit`](../internal/payment/debit_test.go).

## Distributed lock instead of DB-enforced correctness

**Gap:** a Redis lock (`SET key val NX PX 5000`) expires during a GC
stall or a slow network hop, and two workers both believe they hold
exclusive access to the same transaction — the lock's expiry is a wall-clock
guess about how long the critical section takes, not a correctness
guarantee.

**Fix:** correctness here comes from the database, not a lock with a TTL.
`idempotency.Store.Reserve` is described as backed in production by a
Postgres `UNIQUE` constraint on `idempotency_key`
(`INSERT ... ON CONFLICT (idempotency_key) DO NOTHING`) — the database
itself rejects the second concurrent insert, atomically, with no expiry
window. A Redis lock may still be used as a *fast-path optimization* to
avoid hitting Postgres on the common case, but it is never the source of
truth; the unique constraint is what actually prevents two workers from
both proceeding. See the `Store` interface doc comment in
[`internal/idempotency/store.go`](../internal/idempotency/store.go).

## 2PC across bank + wallet instead of saga

**Gap:** a two-phase-commit coordinator spanning our ledger and an
external bank rail (IMPS/NEFT) has to hold the wallet account locked for
the full round trip to the bank. One slow bank response stalls every
other operation on that account, and in practice there is no participant
on the bank's side willing to join our transaction coordinator at all —
2PC across an organizational boundary isn't actually available as an
option.

**Fix:** [`internal/saga/withdraw.go`](../internal/saga/withdraw.go) models
the withdrawal as two local, compensable steps: debit the wallet, then
call the bank. If the bank call fails, a compensating credit reverses the
debit. The account is only ever locked for the duration of one local
step, never for the bank round trip. See
[`TestWithdrawToBank_BankFailureCompensatesWalletDebit`](../internal/saga/withdraw_test.go).

## No per-account partitioning

**Gap:** a balance cache shared across a fleet, mutated from whichever
request goroutine happens to handle a given call, has no single owner for
a given account — two nodes can each believe they're the one processing a
transaction for the same account concurrently.

**Fix:** [`internal/partition/shard.go`](../internal/partition/shard.go)
gives every account exactly one owning goroutine (`Shard.run`); all
mutation goes through a channel to that goroutine. `Ring.ShardFor` uses
consistent hashing so the same account ID always routes to the same
shard/host. See
[`TestRing_ConcurrentDebitsOnSameAccountAreSerialized`](../internal/partition/shard_test.go).

## Eventual consistency applied to the ledger

**Gap:** if the balance shown immediately after a transfer is read from
an asynchronously-replicated cache, the user can see their pre-transfer
balance right after confirming the transfer — a strong-consistency
expectation applied to a system that only offers eventual consistency.

**Fix:** this repo keeps two explicitly different balance sources:
[`internal/wallet/cache.go`](../internal/wallet/cache.go) (`BalanceCache`,
documented as allowed to lag — fast, used for dashboards/listings) and
[`internal/ledger/account.go`](../internal/ledger/account.go)
(`Account.Balance()`, the source of truth, always read-your-writes
consistent because it reads the same mutex-protected int that `Transfer`
just wrote). The rule: any read path that follows a write the user just
made (the confirmation screen right after a transfer) reads from
`ledger.Account`, never from `wallet.BalanceCache`. See
[`JUDGMENT.md`](./JUDGMENT.md) #1 for the incident that would force this
choice.
