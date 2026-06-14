// Scenario 10: Cyclic Dependency Graph Handling
//
// Verifies that a session state containing a cyclic dependency representation
// (e.g., A→B→A expressed as IDs) serializes and deserializes cleanly without
// triggering infinite loops or stack exhaustion.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario10_cyclic_deps
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

// CyclicNode represents one node; Deps holds IDs of nodes it depends on,
// allowing cycles to be expressed without recursive pointers.
type CyclicNode struct {
	ID   string   `json:"id"`
	Deps []string `json:"deps"`
}

type CyclicState struct {
	Nodes []CyclicNode `json:"nodes"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[CyclicState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[CyclicState](store)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[CyclicState]("cyclic-session"),
		session.WithInitialState(CyclicState{}),
		session.WithStore[CyclicState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// Build a cycle: A→B→C→A  and  X→X (self-loop).
	cyclic := CyclicState{
		Nodes: []CyclicNode{
			{ID: "A", Deps: []string{"B"}},
			{ID: "B", Deps: []string{"C"}},
			{ID: "C", Deps: []string{"A"}}, // back-edge — creates cycle
			{ID: "X", Deps: []string{"X"}}, // self-loop
		},
	}
	if err := sess.UpdateState(ctx, cyclic); err != nil {
		fail("set cyclic state: %v", err)
	}

	if err := adapter.Capture(ctx, "cyclic-session",
		"Model the cyclic dependency A→B→C→A",
		"Represented as ID references; no recursive pointers used."); err != nil {
		fmt.Printf("  capture warning: %v\n", err)
	}

	// Reload — if cyclic structure caused any issue it would fail here.
	loaded, err := session.Load(ctx, adapter, "cyclic-session")
	if err != nil {
		fail("load session: %v", err)
	}

	nodes := loaded.State().Nodes
	if len(nodes) != 4 {
		fail("node count mismatch: got %d, want 4", len(nodes))
	}
	// Verify the cycle is intact.
	byID := make(map[string]CyclicNode)
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if byID["C"].Deps[0] != "A" {
		fail("cycle broken: C.deps[0]=%q, want A", byID["C"].Deps[0])
	}
	if byID["X"].Deps[0] != "X" {
		fail("self-loop broken: X.deps[0]=%q, want X", byID["X"].Deps[0])
	}

	fmt.Printf("PASS: cyclic graph (%d nodes) serialized and restored without loops\n",
		len(nodes))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
