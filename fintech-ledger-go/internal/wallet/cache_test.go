package wallet

import (
	"sync"
	"testing"

	"fintechledger/internal/money"
)

// TestApply_ConcurrentDebitsCredits must pass under `go test -race`.
// 1000 goroutines each apply +1 paise and 1000 apply -1 paise
// concurrently; the final balance must be exactly the opening balance
// with no lost updates.
func TestApply_ConcurrentDebitsCredits(t *testing.T) {
	c := NewBalanceCache()
	c.Set("acct-1", money.FromRupees(1000, 0))

	var wg sync.WaitGroup
	const n = 1000
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.Apply("acct-1", money.Paise(1))
		}()
		go func() {
			defer wg.Done()
			c.Apply("acct-1", money.Paise(-1))
		}()
	}
	wg.Wait()

	got, _ := c.Get("acct-1")
	want := money.FromRupees(1000, 0)
	if got != want {
		t.Fatalf("balance drifted under concurrent load: got %s, want %s", got, want)
	}
}
