package loadbalancer

import (
	"fmt"
	"hash/fnv"
	"sort"
)

const virtualNodesPerBackend = 100

type ringNode struct {
	hash    uint32
	backend *Backend
}

// hashRing implements consistent hashing so the same key (e.g. account
// ID) always routes to the same backend, keeping cache locality and
// session affinity — without it, every request for the same account
// could land on a different replica and defeat any per-instance cache.
// Virtual nodes (100 per backend) smooth out the distribution that a
// single hash point per backend would otherwise skew.
type hashRing struct {
	nodes []ringNode
}

func newHashRing(backends []*Backend) *hashRing {
	nodes := make([]ringNode, 0, len(backends)*virtualNodesPerBackend)
	for _, b := range backends {
		for i := 0; i < virtualNodesPerBackend; i++ {
			nodes = append(nodes, ringNode{hash: hashKey(fmt.Sprintf("%s#%d", b.ID, i)), backend: b})
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].hash < nodes[j].hash })
	return &hashRing{nodes: nodes}
}

func hashKey(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}

// get walks the ring clockwise from key's hash, returning the first
// healthy backend it finds. It scans at most len(nodes) entries, so it
// always terminates even if every backend but one is unhealthy.
func (r *hashRing) get(key string) *Backend {
	if len(r.nodes) == 0 {
		return nil
	}
	h := hashKey(key)
	start := sort.Search(len(r.nodes), func(i int) bool { return r.nodes[i].hash >= h })
	for i := 0; i < len(r.nodes); i++ {
		n := r.nodes[(start+i)%len(r.nodes)]
		if n.backend.Healthy() {
			return n.backend
		}
	}
	return nil
}
