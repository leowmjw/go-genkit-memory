// Scenario 20: Repeated Capture/Recall Stability
//
// Exercises repeated local Capture/Recall cycles and verifies they complete
// without errors or panics across multiple turns.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario20_flapping
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

type FlappingState struct {
	Turn int `json:"turn"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	dataDir := filepath.Join("examples", "scenario20_flapping", ".memory")
	if err := os.RemoveAll(dataDir); err != nil {
		fail("reset data dir: %v", err)
	}
	defer os.RemoveAll(dataDir)

	store, err := sqlitestore.NewStore[FlappingState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	cfg := memstore.DefaultPipelineConfig()
	cfg.DataDir = dataDir
	cfg.L1TriggerAfterTurns = 1000

	memStore := memstore.NewInMemoryStore()
	adapter := memstore.NewAdapter[FlappingState](store,
		memstore.WithPipelineConfig(cfg),
		memstore.WithMemoryStore(memStore),
	)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[FlappingState]("flapping-session"),
		session.WithInitialState(FlappingState{}),
		session.WithStore[FlappingState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	const turns = 10

	for i := range turns {
		recalled, err := adapter.Recall(ctx, "flapping-session", "")
		if err != nil {
			fail("turn %d: Recall returned error: %v", i+1, err)
		}
		if recalled != "" {
			fail("turn %d: expected empty recall context, got %q", i+1, recalled)
		}

		if err := adapter.Capture(ctx, "flapping-session",
			fmt.Sprintf("Turn %d user message", i+1),
			fmt.Sprintf("Turn %d assistant reply", i+1)); err != nil {
			fail("turn %d: Capture returned error: %v", i+1, err)
		}

		state := sess.State()
		state.Turn = i + 1
		if err := sess.UpdateState(ctx, state); err != nil {
			fail("turn %d: UpdateState failed: %v", i+1, err)
		}
	}

	if sess.State().Turn != turns {
		fail("expected final turn %d, got %d", turns, sess.State().Turn)
	}
	if buffered := adapter.FallbackLen(); buffered != 0 {
		fail("expected no fallback buffering, got %d entries", buffered)
	}

	fmt.Printf("PASS: %d local Capture/Recall cycles completed without errors\n", turns)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
