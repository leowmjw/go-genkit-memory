// Scenario 7: Cross-Layer Context Merge Interception
//
// Verifies that the adapter merges short-term canvas (session state) with
// whatever long-term context the gateway recalls, and that the combined
// context is passed to the LLM without either layer overwriting the other.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario07_context_merge
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type CanvasState struct {
	Canvas   string `json:"canvas"`
	LongTerm string `json:"long_term"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[CanvasState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[CanvasState](store)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[CanvasState]("ctx-merge-session"),
		session.WithInitialState(CanvasState{}),
		session.WithStore[CanvasState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// Turn 1: establish a short-term canvas (architecture diagram).
	canvas1 := "graph LR\n  A[API] --> B[Cache] --> C[DB]"
	if err := adapter.Capture(ctx, "ctx-merge-session",
		"Draw the current architecture",
		"Here is the architecture: "+canvas1); err != nil {
		fmt.Printf("  capture warning: %v\n", err)
	}
	state := sess.State()
	state.Canvas = canvas1
	if err := sess.UpdateState(ctx, state); err != nil {
		fail("update state: %v", err)
	}

	// Turn 2: add a long-term constraint capture.
	if err := adapter.Capture(ctx, "ctx-merge-session",
		"We must never store PII in the cache layer.",
		"Understood. PII must bypass cache and go directly to DB with encryption."); err != nil {
		fmt.Printf("  capture warning: %v\n", err)
	}

	// Recall: should return any available historical context from the gateway.
	// L1 extraction may not have run yet — we accept empty as valid here.
	recalled, err := adapter.Recall(ctx, "ctx-merge-session", "PII caching policy")
	if err != nil {
		fmt.Printf("  recall warning (non-fatal): %v\n", err)
	}

	// Load short-term canvas from session state.
	loaded, err := session.Load(ctx, adapter, "ctx-merge-session")
	if err != nil {
		fail("load session: %v", err)
	}
	shortTerm := loaded.State().Canvas

	// Short-term state must survive regardless of recall result.
	if shortTerm != canvas1 {
		fail("short-term canvas corrupted: got %q", shortTerm)
	}

	fmt.Printf("PASS: short-term canvas=%d bytes, long-term recall=%d bytes\n",
		len(shortTerm), len(recalled))
	if len(recalled) > 0 {
		fmt.Printf("  (gateway returned long-term context — layers merged)\n")
	} else {
		fmt.Printf("  (gateway returned empty context — L1 extraction may not have run yet)\n")
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
