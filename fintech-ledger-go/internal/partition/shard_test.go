package partition

import (
	"context"
	"errors"
	"sync"
	"testing"

	"fintechledger/internal/ledger"
	"fintechledger/internal/money"
)

// TestRing_ConcurrentDebitsOnSameAccountAreSerialized proves the
// single-owner guarantee: 1000 concurrent -1 paise debits routed through
// the ring at the same account always land on the same shard goroutine,
// so none are lost to a race — unlike a bare shared map touched from
// every caller's own goroutine.
func TestRing_ConcurrentDebitsOnSameAccountAreSerialized(t *testing.T) {
	accounts := []*ledger.Account{
		ledger.NewAccount("acct-1", money.FromRupees(1000, 0)),
		ledger.NewAccount("acct-2", money.FromRupees(1000, 0)),
	}
	shard1, shard2 := NewShard(accounts[:1]), NewShard(accounts[1:])
	defer shard1.Close()
	defer shard2.Close()
	ring := NewRing([]*Shard{shard1, shard2})

	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 1000
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			shard := ring.ShardFor("acct-1")
			_ = shard.Do(ctx, "acct-1", func(a *ledger.Account) {
				_ = a.Debit(money.Paise(1))
			})
		}()
	}
	wg.Wait()

	want := money.FromRupees(1000, 0).Sub(money.Paise(n))
	if got := accounts[0].Balance(); got != want {
		t.Fatalf("balance = %s, want %s (lost updates under concurrent access)", got, want)
	}
}

func TestRing_SameAccountAlwaysRoutesToSameShard(t *testing.T) {
	accounts := []*ledger.Account{
		ledger.NewAccount("acct-1", money.FromRupees(0, 0)),
	}
	s1, s2, s3 := NewShard(accounts), NewShard(nil), NewShard(nil)
	defer s1.Close()
	defer s2.Close()
	defer s3.Close()
	ring := NewRing([]*Shard{s1, s2, s3})

	first := ring.ShardFor("acct-1")
	for i := 0; i < 100; i++ {
		if ring.ShardFor("acct-1") != first {
			t.Fatal("routing for the same account ID must be stable")
		}
	}
}

// TestShard_DoReportsUnknownAccount guards against a routing bug being
// silently swallowed: calling Do for an account this shard doesn't own
// must return ErrUnknownAccount, not silently no-op.
func TestShard_DoReportsUnknownAccount(t *testing.T) {
	s := NewShard(nil)
	defer s.Close()

	called := false
	err := s.Do(context.Background(), "does-not-exist", func(a *ledger.Account) { called = true })
	if !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("got %v, want ErrUnknownAccount", err)
	}
	if called {
		t.Fatal("fn must not run for an account the shard does not own")
	}
}

// TestShard_CloseStopsTheOwningGoroutine proves Close actually shuts the
// run goroutine down: a Do call after Close must not hang forever, and
// a repeated send on a closed channel must not panic the caller.
func TestShard_CloseStopsTheOwningGoroutine(t *testing.T) {
	acc := ledger.NewAccount("acct-1", money.FromRupees(0, 0))
	s := NewShard([]*ledger.Account{acc})
	s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		if r := recover(); r == nil {
			t.Fatal("expected a panic (send on closed channel) proving the goroutine actually stopped consuming")
		}
	}()
	_ = s.Do(ctx, "acct-1", func(a *ledger.Account) {})
}
