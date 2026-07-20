// Package concurrency enforces per-account device limits (EXACT) and reports
// global concurrency (APPROXIMATE, via HLL). See ADR-001: exactness is only
// needed at single-account granularity, so it fits in one shard and the lock
// is never global.
package concurrency

import (
	"hash/maphash"
	"sync"
)

// Manager holds sharded per-account device sets. A device set is
// deviceID -> expiry(unix nanos); admission and release always happen under the
// owning shard's lock, so a single account serializes while unrelated accounts
// never contend.
type Manager struct {
	shards []*shard
	mask   uint64
	seed   maphash.Seed
	hll    *Estimator
}

type shard struct {
	mu   sync.Mutex
	acct map[string]*account
}

type account struct {
	limit   int
	devices map[string]*device // deviceID -> live device
}

type device struct {
	expiresAt int64  // unix nanos
	sessionID string // the session that owns this device slot (1:1)
}

// reapExpiredLocked drops this account's devices whose expiry <= nowNano. Caller
// must hold the shard lock. O(limit) — cheap, and it closes the sweeper-lag
// window (M1) so a stopped device never falsely blocks a new one.
func (a *account) reapExpiredLocked(nowNano int64) {
	for id, d := range a.devices {
		if d.expiresAt <= nowNano {
			delete(a.devices, id)
		}
	}
}

// New builds a Manager with shardCount shards (must be a power of two).
func New(shardCount int) *Manager {
	if shardCount < 1 || shardCount&(shardCount-1) != 0 {
		shardCount = 256
	}
	m := &Manager{
		shards: make([]*shard, shardCount),
		mask:   uint64(shardCount - 1),
		seed:   maphash.MakeSeed(),
		hll:    NewEstimator(),
	}
	for i := range m.shards {
		m.shards[i] = &shard{acct: make(map[string]*account)}
	}
	return m
}

func (m *Manager) shardFor(accountID string) *shard {
	h := maphash.String(m.seed, accountID)
	return m.shards[h&m.mask]
}

// Admit registers deviceID under accountID if the account is below limit. It is
// idempotent per (account, device): re-admitting an existing device refreshes
// its expiry and returns that device's ORIGINAL sessionID — it never mints a
// second session for one device (H1). newSessionID is used only when the device
// is genuinely new. Returns (effectiveSessionID, admitted). This is the
// device-limit guardrail: it MUST NOT admit the (limit+1)-th distinct device.
func (m *Manager) Admit(accountID, deviceID string, limit int, nowNano, expiresAtNano int64, newSessionID string) (string, bool) {
	s := m.shardFor(accountID)
	s.mu.Lock()
	a, ok := s.acct[accountID]
	if !ok {
		a = &account{limit: limit, devices: make(map[string]*device, limit)}
		s.acct[accountID] = a
	}
	a.limit = limit              // trust latest signed claim
	a.reapExpiredLocked(nowNano) // M1: close the sweeper-lag window
	if d, present := a.devices[deviceID]; present {
		d.expiresAt = expiresAtNano // idempotent refresh, keep original session
		sid := d.sessionID
		s.mu.Unlock()
		return sid, true
	}
	if len(a.devices) >= a.limit {
		s.mu.Unlock()
		return "", false // over limit — exact rejection
	}
	a.devices[deviceID] = &device{expiresAt: expiresAtNano, sessionID: newSessionID}
	s.mu.Unlock()
	m.hll.Add(accountID) // approximate global signal, lock-free
	return newSessionID, true
}

// Refresh extends a device's expiry. Returns false if the device is not present
// (e.g. already reclaimed by the sweeper).
func (m *Manager) Refresh(accountID, deviceID string, expiresAtNano int64) bool {
	s := m.shardFor(accountID)
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.acct[accountID]
	if !ok {
		return false
	}
	d, present := a.devices[deviceID]
	if !present {
		return false
	}
	d.expiresAt = expiresAtNano
	return true
}

// Release removes a device (explicit stop). Idempotent.
func (m *Manager) Release(accountID, deviceID string) {
	s := m.shardFor(accountID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.acct[accountID]; ok {
		delete(a.devices, deviceID)
		if len(a.devices) == 0 {
			delete(s.acct, accountID)
		}
	}
}

// ReapExpired drops every device whose expiry is <= nowNano across all shards
// and returns the number reclaimed. Called by the session sweeper.
func (m *Manager) ReapExpired(nowNano int64) int {
	reclaimed := 0
	for _, s := range m.shards {
		s.mu.Lock()
		for id, a := range s.acct {
			for dev, d := range a.devices {
				if d.expiresAt <= nowNano {
					delete(a.devices, dev)
					reclaimed++
				}
			}
			if len(a.devices) == 0 {
				delete(s.acct, id)
			}
		}
		s.mu.Unlock()
	}
	return reclaimed
}

// CountFor returns the exact live device count for an account (test/debug).
func (m *Manager) CountFor(accountID string) int {
	s := m.shardFor(accountID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.acct[accountID]; ok {
		return len(a.devices)
	}
	return 0
}

// GlobalEstimate returns the approximate number of distinct active accounts.
func (m *Manager) GlobalEstimate() float64 { return m.hll.Estimate() }
