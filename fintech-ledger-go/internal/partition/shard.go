// Package partition gives every account exactly one owner: a single
// goroutine that serializes all commands against the accounts in its
// shard.
package partition

import (
	"context"
	"errors"
	"hash/fnv"

	"fintechledger/internal/ledger"
)

var ErrUnknownAccount = errors.New("partition: account is not owned by this shard")

type command struct {
	accountID string
	fn        func(*ledger.Account)
	done      chan error
}

// Shard owns a fixed set of accounts. All mutation goes through Do,
// which enqueues a command onto a channel read by a single goroutine
// (run). That goroutine is the only thing in the process that ever
// touches these accounts directly — a shared in-memory map touched from
// every request-handling goroutine across a fleet has no single owner
// for a given account, which is exactly what makes it unsafe: two nodes
// can process what they each believe is the same transaction
// concurrently. A shard, replicated per hostname behind Ring, gives each
// account one owner across the whole fleet.
type Shard struct {
	accounts map[string]*ledger.Account
	commands chan command
}

func NewShard(accounts []*ledger.Account) *Shard {
	m := make(map[string]*ledger.Account, len(accounts))
	for _, a := range accounts {
		m[a.ID] = a
	}
	s := &Shard{accounts: m, commands: make(chan command, 64)}
	go s.run()
	return s
}

func (s *Shard) run() {
	for cmd := range s.commands {
		acc, ok := s.accounts[cmd.accountID]
		if !ok {
			cmd.done <- ErrUnknownAccount
			close(cmd.done)
			continue
		}
		cmd.fn(acc)
		close(cmd.done)
	}
}

// Do runs fn against the named account on the shard's owning goroutine
// and blocks until it completes or ctx is done. It returns
// ErrUnknownAccount if accountID is not owned by this shard — silently
// no-op'ing on a routing mistake would hide a bug where a caller bypassed
// Ring.ShardFor and mutated the wrong shard.
func (s *Shard) Do(ctx context.Context, accountID string, fn func(*ledger.Account)) error {
	done := make(chan error, 1)
	select {
	case s.commands <- command{accountID: accountID, fn: fn, done: done}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the shard's owning goroutine. Callers must not call Do
// after Close. Without this, every Shard created over the life of a
// process (e.g. one per test, or a resharding event) leaks its run
// goroutine forever.
func (s *Shard) Close() {
	close(s.commands)
}

// Ring routes an account ID to the same shard every time via consistent
// hashing, so the account always has the same owner.
type Ring struct {
	shards []*Shard
}

func NewRing(shards []*Shard) *Ring {
	return &Ring{shards: shards}
}

func (r *Ring) ShardFor(accountID string) *Shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(accountID))
	return r.shards[h.Sum32()%uint32(len(r.shards))]
}
