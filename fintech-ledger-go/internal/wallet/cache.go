// Package wallet holds a fast, front-of-ledger balance cache. It is
// explicitly allowed to lag the ledger by design (see docs/DESIGN.md,
// "eventual consistency applied to the ledger") — callers that need the
// authoritative, just-written balance must read from ledger.Account
// directly, not this cache.
package wallet

import (
	"sync"

	"fintechledger/internal/money"
)

// BalanceCache is a concurrency-safe in-memory cache of wallet balances.
// A plain `map[string]money.Paise` mutated from multiple request
// goroutines (concurrent debit/credit updates with no synchronization) is
// a data race: the Go runtime detects concurrent map writes and can crash
// the process, and even reads racing with writes can observe a
// half-written balance. Every access here goes through a single mutex.
type BalanceCache struct {
	mu       sync.RWMutex
	balances map[string]money.Paise
}

func NewBalanceCache() *BalanceCache {
	return &BalanceCache{balances: make(map[string]money.Paise)}
}

func (c *BalanceCache) Get(accountID string) (money.Paise, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.balances[accountID]
	return v, ok
}

func (c *BalanceCache) Set(accountID string, bal money.Paise) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balances[accountID] = bal
}

// Apply adds delta (positive for credit, negative for debit) to the
// cached balance and returns the new value.
func (c *BalanceCache) Apply(accountID string, delta money.Paise) money.Paise {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balances[accountID] = c.balances[accountID].Add(delta)
	return c.balances[accountID]
}
