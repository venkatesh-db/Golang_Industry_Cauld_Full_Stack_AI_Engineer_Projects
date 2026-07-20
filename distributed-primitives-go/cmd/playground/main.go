// Command playground serves an interactive UI over the REAL raft.Cluster
// so you can click to elect leaders, split the network, heal it, and
// watch quorum + fencing behave. Every button drives the actual Go
// implementation in package raft — nothing is faked in the browser.
//
//	go run ./cmd/playground   (or PORT=9100 go run ./cmd/playground)
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"distributedprimitives/raft"
)

type server struct {
	mu      sync.Mutex
	cluster *raft.Cluster
	size    int
	logs    []string
}

func (s *server) note(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	s.logs = append([]string{line}, s.logs...)
	if len(s.logs) > 12 {
		s.logs = s.logs[:12]
	}
}

type stateResp struct {
	Nodes         []raft.NodeView `json:"nodes"`
	CommittedTerm uint64          `json:"committedTerm"`
	Logs          []string        `json:"logs"`
}

func (s *server) state() stateResp {
	nodes, ct := s.cluster.Snapshot()
	return stateResp{Nodes: nodes, CommittedTerm: ct, Logs: s.logs}
}

func (s *server) writeState(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.state())
}

func (s *server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeState(w)
}

func (s *server) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster = raft.NewCluster(s.size)
	s.logs = nil
	s.note("reset: fresh %d-node cluster", s.size)
	s.writeState(w)
}

func (s *server) handleElect(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	leader, err := s.cluster.Elect(id)
	if err != nil {
		s.note("node %d tried to elect → %v", id, err)
	} else {
		s.note("node %d became LEADER at term %d", leader.ID, leader.Term)
	}
	s.writeState(w)
}

func (s *server) handlePartition(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := strings.Split(r.URL.Query().Get("ids"), ",")
	var ids []int
	for _, p := range raw {
		if p == "" {
			continue
		}
		if id, err := strconv.Atoi(p); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		s.note("partition: no nodes selected")
	} else {
		s.cluster.Partition(ids...)
		s.note("partitioned nodes %v into their own group", ids)
	}
	s.writeState(w)
}

func (s *server) handleHeal(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster.Heal()
	s.note("network healed — all nodes reconnected")
	s.writeState(w)
}

func (s *server) handleWrite(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	err := s.cluster.Write(id, func() {})
	if err != nil {
		s.note("write via node %d → REJECTED: %v", id, err)
	} else {
		s.note("write via node %d → committed ✓", id)
	}
	s.writeState(w)
}

func main() {
	size := 5
	s := &server{cluster: raft.NewCluster(size), size: size}
	s.note("fresh %d-node cluster ready", size)

	http.HandleFunc("/api/state", s.handleState)
	http.HandleFunc("/api/reset", s.handleReset)
	http.HandleFunc("/api/elect", s.handleElect)
	http.HandleFunc("/api/partition", s.handlePartition)
	http.HandleFunc("/api/heal", s.handleHeal)
	http.HandleFunc("/api/write", s.handleWrite)
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})

	addr := ":9100"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	fmt.Printf("raft playground on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
