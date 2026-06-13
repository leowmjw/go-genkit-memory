// Scenario 1: Multi-Turn Complex Debugging Agent (Short-Term Canvas Test)
//
// Verifies that large raw log payloads (>50 KB) are offloaded to refs/*.md
// while a compact Mermaid canvas is kept in the session state.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario01_debugging
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

type DebugState struct {
	Canvas   string   `json:"canvas"`    // compact Mermaid diagram
	LogRefs  []string `json:"log_refs"`  // paths to offloaded log files
	Step     int      `json:"step"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()
	refsDir := os.TempDir()

	store, err := sqlitestore.NewStore[DebugState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[DebugState](store,
		memstore.WithRefsDir(refsDir),
	)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[DebugState]("debug-session-1"),
		session.WithInitialState(DebugState{}),
		session.WithStore[DebugState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// Simulate 3 debugging turns. Turn 2 has a massive log dump (>50 KB).
	turns := []struct{ user, assistant string }{
		{
			"Service is down. Here is the error: 'connection refused port 5432'",
			"graph LR\n  A[HTTP 503] --> B[DB conn refused]\n  B --> C[postgres:5432]",
		},
		{
			// >50 KB log dump — should be offloaded
			"Full container log: " + strings.Repeat("ERROR: timeout waiting for lock at pg_locks row_id=", 1100),
			"graph LR\n  A[HTTP 503] --> B[DB lock contention]\n  B --> C[Long-running txn detected]",
		},
		{
			"Killed long-running transaction. Is the service recovering?",
			"graph LR\n  A[Txn killed] --> B[Lock released] --> C[Service recovering]",
		},
	}

	var lastCanvas string
	for i, t := range turns {
		// Offload happens transparently inside Capture for large payloads.
		if err := adapter.Capture(ctx, "debug-session-1", t.user, t.assistant); err != nil {
			// Capture errors are non-fatal (fallback buffer).
			fmt.Printf("  turn %d capture warning: %v\n", i+1, err)
		}

		state := sess.State()
		state.Canvas = t.assistant
		state.Step = i + 1
		if err := sess.UpdateState(ctx, state); err != nil {
			fail("update state turn %d: %v", i+1, err)
		}
		lastCanvas = t.assistant
	}

	// Reload and verify the canvas is the compact Mermaid diagram (not a log dump).
	loaded, err := session.Load(ctx, adapter, "debug-session-1")
	if err != nil {
		fail("load session: %v", err)
	}

	canvas := loaded.State().Canvas
	if canvas != lastCanvas {
		fail("canvas mismatch: want %q, got %q", lastCanvas, canvas)
	}
	if len(canvas) > 300 {
		fail("canvas too large (%d bytes) — should be compact Mermaid", len(canvas))
	}

	fmt.Printf("PASS: canvas length=%d bytes, session step=%d\n",
		len(canvas), loaded.State().Step)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
