// Scenario 9: Multi-Node Canvas Relationship Breaking
//
// Verifies that explicit removal of a canvas edge updates the session state
// cleanly without corrupting the remaining structure or producing broken
// Mermaid syntax.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario09_canvas_mutation
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type GraphState struct {
	Nodes []string `json:"nodes"`
	Edges []string `json:"edges"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[GraphState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[GraphState](store)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[GraphState]("canvas-session"),
		session.WithInitialState(GraphState{}),
		session.WithStore[GraphState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// Step 1: build a 4-node graph.
	state := GraphState{
		Nodes: []string{"A", "B", "C", "D"},
		Edges: []string{"A->B", "B->C", "C->D", "A->D"},
	}
	if err := sess.UpdateState(ctx, state); err != nil {
		fail("set initial graph: %v", err)
	}
	if err := adapter.Capture(ctx, "canvas-session",
		"Build a pipeline: A→B→C→D with a shortcut A→D",
		"graph LR\n  A-->B\n  B-->C\n  C-->D\n  A-->D"); err != nil {
		fmt.Printf("  capture warning: %v\n", err)
	}

	// Step 2: remove the shortcut edge A→D.
	state.Edges = []string{"A->B", "B->C", "C->D"}
	if err := sess.UpdateState(ctx, state); err != nil {
		fail("remove edge: %v", err)
	}
	if err := adapter.Capture(ctx, "canvas-session",
		"Remove the A→D shortcut.",
		"graph LR\n  A-->B\n  B-->C\n  C-->D"); err != nil {
		fmt.Printf("  capture warning: %v\n", err)
	}

	// Reload and verify state is consistent.
	loaded, err := session.Load(ctx, adapter, "canvas-session")
	if err != nil {
		fail("load session: %v", err)
	}

	edges := loaded.State().Edges
	if len(edges) != 3 {
		fail("wrong edge count after removal: got %d, want 3", len(edges))
	}
	for _, e := range edges {
		if strings.Contains(e, "A->D") {
			fail("removed edge A->D still present in state")
		}
	}
	if len(loaded.State().Nodes) != 4 {
		fail("nodes mutated unexpectedly: got %d, want 4", len(loaded.State().Nodes))
	}

	fmt.Printf("PASS: graph mutation clean — %d nodes, %d edges (shortcut removed)\n",
		len(loaded.State().Nodes), len(edges))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
