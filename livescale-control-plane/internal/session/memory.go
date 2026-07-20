package session

import (
	"hash/maphash"
	"sync"
)

// Memory is a sharded, in-memory Store. Sharding keeps session writes from
// contending on one lock under the herd, mirroring the concurrency manager.
type Memory struct {
	shards []*memShard
	mask   uint64
	seed   maphash.Seed
}

type memShard struct {
	mu sync.RWMutex
	m  map[string]Session
}

// NewMemory builds a sharded in-memory store (shardCount must be power of two).
func NewMemory(shardCount int) *Memory {
	if shardCount < 1 || shardCount&(shardCount-1) != 0 {
		shardCount = 256
	}
	s := &Memory{
		shards: make([]*memShard, shardCount),
		mask:   uint64(shardCount - 1),
		seed:   maphash.MakeSeed(),
	}
	for i := range s.shards {
		s.shards[i] = &memShard{m: make(map[string]Session)}
	}
	return s
}

func (s *Memory) shardFor(id string) *memShard {
	return s.shards[maphash.String(s.seed, id)&s.mask]
}

func (s *Memory) Put(sessionID string, sess Session) {
	sh := s.shardFor(sessionID)
	sh.mu.Lock()
	sh.m[sessionID] = sess
	sh.mu.Unlock()
}

func (s *Memory) Extend(sessionID string, newExpiry int64) (Session, bool) {
	sh := s.shardFor(sessionID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sess, ok := sh.m[sessionID]
	if !ok {
		return Session{}, false
	}
	sess.ExpiresAt = newExpiry
	sh.m[sessionID] = sess
	return sess, true
}

func (s *Memory) Get(sessionID string, nowNano int64) (Session, bool) {
	sh := s.shardFor(sessionID)
	sh.mu.RLock()
	sess, ok := sh.m[sessionID]
	sh.mu.RUnlock()
	if !ok || sess.ExpiresAt <= nowNano { // lazy expiry on read
		return Session{}, false
	}
	return sess, true
}

func (s *Memory) Delete(sessionID string) (Session, bool) {
	sh := s.shardFor(sessionID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sess, ok := sh.m[sessionID]
	if ok {
		delete(sh.m, sessionID)
	}
	return sess, ok
}

// ReapExpired deletes sessions whose expiry <= nowNano and returns the expired
// ones so the caller can decrement device counts. Used by the sweeper.
func (s *Memory) ReapExpired(nowNano int64) []Session {
	var expired []Session
	for _, sh := range s.shards {
		sh.mu.Lock()
		for id, sess := range sh.m {
			if sess.ExpiresAt <= nowNano {
				expired = append(expired, sess)
				delete(sh.m, id)
			}
		}
		sh.mu.Unlock()
	}
	return expired
}
