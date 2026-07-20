package pool

import (
	"strings"
	"testing"
)

func TestGetPut_BufferIsReset(t *testing.T) {
	buf := Get()
	buf.WriteString("leftover data")
	Put(buf)

	buf2 := Get()
	if buf2.Len() != 0 {
		t.Fatalf("buffer from pool should be reset, has %d bytes", buf2.Len())
	}
}

func TestPut_DropsOversizedBuffers(t *testing.T) {
	buf := Get()
	buf.Write(make([]byte, 128*1024)) // grow well past maxPooledCapacity
	oversizedCap := buf.Cap()
	Put(buf)

	// Drain the pool a few times; none of the buffers we get back should
	// carry the oversized capacity forward.
	for i := 0; i < 8; i++ {
		b := Get()
		if b.Cap() >= oversizedCap {
			t.Fatalf("oversized buffer (cap=%d) was pooled instead of dropped", b.Cap())
		}
		Put(b)
	}
}

// payload is precomputed once so both benchmarks below measure only the
// cost of the buffer itself, not string construction inside the loop
// (building the string fresh every iteration would allocate identically
// in both variants and mask any difference between them).
var payload = []byte(strings.Repeat("x", 512))

// sink holds a reference to each iteration's slice (not just a derived
// scalar) so the compiler can't prove the allocation stays local and
// stack-allocate it — which is exactly what defeated an earlier version
// of this benchmark that only fed a length into an atomic counter: with
// nothing but a copied int leaving the loop, escape analysis legally
// stack-allocated `make([]byte, ...)` and made BenchmarkWithoutPool look
// as cheap as the pooled version, which isn't representative of real
// usage where the buffer itself (written to a connection, returned to a
// caller) actually escapes. Sequential (not RunParallel) so a plain
// package-level assignment isn't a data race.
var sink []byte

func BenchmarkGetPut(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := Get()
		buf.Write(payload)
		sink = buf.Bytes()
		Put(buf)
	}
}

func BenchmarkWithoutPool(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, 0, defaultBufferCapacity)
		buf = append(buf, payload...)
		sink = buf
	}
}
