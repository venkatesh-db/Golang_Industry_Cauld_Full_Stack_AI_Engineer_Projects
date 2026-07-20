// Command demo prints the headline behavior of each primitive so you can
// run one binary in an interview and narrate what it proves.
//
//	go run ./cmd/demo
package main

import (
	"fmt"

	"distributedprimitives/clocks"
	"distributedprimitives/raft"
	"distributedprimitives/replication"
)

func main() {
	fmt.Println("== 1. Logical clocks: order without a global clock ==")
	a := clocks.NewVectorClock("a")
	b := clocks.NewVectorClock("b")
	a.Tick()
	b.Tick()
	fmt.Printf("independent writes on a,b => %s (must resolve as conflict)\n",
		clocks.Compare(a.Snapshot(), b.Snapshot()))
	b.Merge(a)
	fmt.Printf("after b receives a       => a is %s b (causality captured)\n\n",
		clocks.Compare(a.Snapshot(), b.Snapshot()))

	fmt.Println("== 2. Multi-leader conflict: LWW loses data, CRDT does not ==")
	us := replication.LWWRegister{Value: "alice(first, clock +10s)", TS: 10000, Node: "us"}
	eu := replication.LWWRegister{Value: "bob(actually later)", TS: 9500, Node: "eu"}
	fmt.Printf("LWW survivor  => %q  <- the later write was silently dropped\n",
		replication.LWWOutcome(us, eu).Value)
	gUS, gEU := replication.NewGCounter(), replication.NewGCounter()
	gUS.Inc("us", 3)
	gEU.Inc("eu", 4)
	fmt.Printf("CRDT total    => %d  <- every increment preserved\n\n",
		replication.CRDTOutcome(gUS, gEU))

	fmt.Println("== 3. Consensus: quorum + fencing prevent split-brain ==")
	c := raft.NewCluster(5)
	l1, _ := c.Elect(0)
	fmt.Printf("elected leader node %d at term %d\n", l1.ID, l1.Term)
	c.Partition(0)
	if _, err := c.Elect(0); err != nil {
		fmt.Printf("isolated old leader re-elects => %v (minority blocked)\n", err)
	}
	l2, _ := c.Elect(1)
	fmt.Printf("majority elects node %d at term %d\n", l2.ID, l2.Term)
	c.Heal()
	fmt.Printf("healed: stale leader write => %v\n", c.Write(l1.ID, func() {}))
	fmt.Printf("current leader write       => %v\n", c.Write(l2.ID, func() {}))
}
