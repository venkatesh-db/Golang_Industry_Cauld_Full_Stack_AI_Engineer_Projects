// Package pool reuses byte buffers across requests so the hot request
// path (reading request bodies, building proxied requests, encoding
// responses) doesn't allocate and immediately discard a buffer per
// request. At high request rates, that allocation churn is GC pressure
// that shows up directly as tail latency — sync.Pool lets the runtime
// reuse buffers already sized for the common case instead.
package pool

import (
	"bytes"
	"sync"
)

const defaultBufferCapacity = 4096

var bufPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, defaultBufferCapacity))
	},
}

// Get returns a reset, ready-to-use buffer. Callers must call Put when
// done to return it to the pool.
func Get() *bytes.Buffer {
	return bufPool.Get().(*bytes.Buffer)
}

// Put resets buf and returns it to the pool. Buffers that grew far
// beyond the default capacity are dropped instead of pooled, so one
// outsized request (a large payment webhook body) doesn't permanently
// bloat the pool's steady-state memory for every future buffer reuse.
func Put(buf *bytes.Buffer) {
	const maxPooledCapacity = 64 * 1024
	if buf.Cap() > maxPooledCapacity {
		return
	}
	buf.Reset()
	bufPool.Put(buf)
}
