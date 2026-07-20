# Judgment calls

These are the three "own the trade-off" scenarios, written as case
studies against the code in this repo.

## 1. Choosing eventual consistency to cut latency, then moving one read path back to strong consistency

The `GET /wallet/:id/balance` endpoint used for dashboards and account
listings should read from
[`wallet.BalanceCache`](../internal/wallet/cache.go): it's an in-memory
cache updated asynchronously off the ledger's write path, so it can serve
thousands of reads per second without touching `ledger.Account`'s mutex
at all. That's the right default — most balance reads aren't immediately
downstream of a write the same user just made, and paying strong-read
latency on all of them is waste.

The complaint that forces a change: users who just completed a transfer
land on a confirmation screen and see their *old* balance, because that
screen's `GET /balance` call raced the cache's async update. The fix
isn't to make the cache strongly consistent everywhere (that reintroduces
the latency cost for the 99% of reads that didn't need it) — it's to
identify that *this one read path* (the screen immediately following a
write) needs strong consistency, and route only that path to
`ledger.Account.Balance()` instead, which is always read-your-writes
consistent because `Transfer` and `Balance()` share the same mutex. Every
other balance read stays on the cache.

The judgment isn't "pick consistency model X" — it's recognizing that
consistency is a per-read-path decision, not a system-wide one, and being
able to name which specific path just violated a user's expectation.

## 2. A "quick fix" retry added under incident pressure that caused a double-charge storm

Incident scenario: the payment gateway starts timing out intermittently.
An on-call engineer, under pressure, wraps the debit call in a bare retry
loop —

```go
for {
    err := gatewayClient.Debit(accountID, amount)
    if err == nil {
        break
    }
}
```

— and ships it as a hotfix. This looks like it fixes the timeouts. What
it actually does: every timeout (which doesn't tell you whether the debit
landed on the gateway's side before the response was lost) now retries a
non-idempotent debit call directly, with no cap and no de-duplication.
Under a gateway blip affecting many requests at once, this is a
double-charge storm, not a fix — and it's also an unbounded-retry storm
against a gateway that's already struggling (the exact failure mode
`gateway.CallWithRetry`'s attempt cap and backoff exist to prevent).

The actual fix in this repo composes two things that must never be
separated: [`gateway.CallWithRetry`](../internal/gateway/client.go) (bounded
attempts, capped exponential backoff — this is what's safe to retry
blindly) wrapping a call whose *effect* is idempotent —
[`payment.Debit`](../internal/payment/debit.go), keyed by
`Idempotency-Key`. Retrying the idempotent operation is safe no matter
how many times it's retried; retrying the raw non-idempotent debit is
what causes the storm.

The judgment call, recognized under pressure: "add a retry" is not itself
a fix if you haven't first confirmed the operation being retried is
idempotent. If it isn't, the retry has to go through the idempotency
layer, not around it.

## 3. Deciding what to reconcile-after vs guarantee-live

Not every write in a payment system needs the same guarantee. The rule
used in this repo: **a write is synchronous and guaranteed-live only if a
user-visible or money-moving invariant depends on it being correct before
the response returns.** Everything else is a candidate for
reconcile-after.

- **Guaranteed live:** `payment.Debit` — the account balance must be
  correct before the API responds, because the caller may act on the
  response (show a confirmation, allow a subsequent spend). This is
  exactly why it's built synchronous and idempotent rather than queued.
- **Safe to reconcile-after:** `reconciliation.Reconcile` — matching our
  transaction log against the bank's end-of-day statement file, computing
  which cashback/fee ledger entries should have posted, generating
  statement PDFs. None of these gate a user-facing response; a mismatch
  found at 11pm during the batch run is still actionable the next
  morning, and holding every debit request synchronously until the bank
  statement is available would be waiting on data that doesn't exist yet.

The line isn't "money vs not-money" — the bank-statement reconciliation
run *is* about money. The line is whether a caller is blocked on the
answer right now. `reconciliation.Reconcile`'s bounded worker pool
([`internal/reconciliation/worker.go`](../internal/reconciliation/worker.go))
is explicitly a nightly batch job: no request thread is waiting on it,
so it's allowed to take minutes and to fail a subset of transactions for
manual follow-up rather than blocking anything live.
