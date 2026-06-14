// Scenario 23: Total Session Garbage Collection & Cold-Start Reconstitution
//
// Verifies that a session persisted to a file-backed SQLite store can be
// fully reconstituted by a new adapter instance after the original adapter
// is closed (simulating a GC / process restart). No context records should
// be dropped.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario23_session_gc
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type PersistedState struct {
	Notes []string `json:"notes"`
	Turn  int      `json:"turn"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()
	dbPath := filepath.Join(os.TempDir(), "gc-test-sessions.db")
	defer os.Remove(dbPath)

	const sessionID = "gc-test-session"

	// ── Phase 1: write session to disk ───────────────────────────────────────
	{
		store, err := sqlitestore.NewStore[PersistedState](ctx, dbPath)
		if err != nil {
			fail("phase1 open store: %v", err)
		}

		adapter := memstore.NewAdapter[PersistedState](store)

		sess, err := session.New(ctx,
			session.WithID[PersistedState](sessionID),
			session.WithInitialState(PersistedState{}),
			session.WithStore[PersistedState](adapter),
		)
		if err != nil {
			fail("phase1 create session: %v", err)
		}

		notes := []string{
			"Design invariant: UUID v4 primary keys only.",
			"Cache layer must not store PII.",
			"All writes must be idempotent.",
		}
		for i, note := range notes {
			if err := adapter.Capture(ctx, sessionID,
				fmt.Sprintf("Note %d: %s", i+1, note),
				"Noted."); err != nil {
				fmt.Printf("  phase1 capture warning: %v\n", err)
			}
			state := sess.State()
			state.Notes = append(state.Notes, note)
			state.Turn = i + 1
			if err := sess.UpdateState(ctx, state); err != nil {
				fail("phase1 update state: %v", err)
			}
		}

		// Simulate GC: close everything.
		adapter.Close()
		store.Close()
		fmt.Printf("  phase1: wrote %d notes to %s, closed store\n", len(notes), dbPath)
	}

	// ── Phase 2: cold-start reconstitution ───────────────────────────────────
	{
		store2, err := sqlitestore.NewStore[PersistedState](ctx, dbPath)
		if err != nil {
			fail("phase2 open store: %v", err)
		}
		defer store2.Close()

		adapter2 := memstore.NewAdapter[PersistedState](store2)
		defer adapter2.Close()

		loaded, err := session.Load(ctx, adapter2, sessionID)
		if err != nil {
			fail("phase2 cold-start load: %v", err)
		}

		state := loaded.State()
		if state.Turn != 3 {
			fail("cold-start lost turns: got turn=%d, want 3", state.Turn)
		}
		if len(state.Notes) != 3 {
			fail("cold-start lost notes: got %d, want 3", len(state.Notes))
		}

		fmt.Printf("PASS: cold-start reconstituted %d notes at turn=%d from %s\n",
			len(state.Notes), state.Turn, filepath.Base(dbPath))
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
