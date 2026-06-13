// Scenario 13: Session Token Leakage & Isolation (100 Concurrent Sessions)
//
// Spawns 100 goroutines each managing a completely isolated session, then
// cross-checks that no session can read another session's data.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario13_isolation
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type IsoState struct {
	Secret string   `json:"secret"`
	Tokens []string `json:"tokens"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	store, err := sqlitestore.NewStore[IsoState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[IsoState](store)
	defer adapter.Close()

	const workers = 100

	// Phase 1: each worker writes its own unique secret.
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(idx int) {
			defer wg.Done()
			sessID := fmt.Sprintf("iso-session-%d", idx)
			secret := fmt.Sprintf("SECRET-WORKER-%d-TOKEN", idx)

			sess, err := session.New(ctx,
				session.WithID[IsoState](sessID),
				session.WithInitialState(IsoState{Secret: secret}),
				session.WithStore[IsoState](adapter),
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "worker %d session.New: %v\n", idx, err)
				return
			}
			state := sess.State()
			state.Tokens = append(state.Tokens, secret)
			_ = sess.UpdateState(ctx, state)
		}(i)
	}
	wg.Wait()

	// Phase 2: each worker reads its own session and verifies no cross-contamination.
	var leaks int
	for i := range workers {
		sessID := fmt.Sprintf("iso-session-%d", i)
		loaded, err := session.Load(ctx, adapter, sessID)
		if err != nil {
			fail("load session %d: %v", i, err)
		}
		s := loaded.State()

		// Own secret must be present.
		ownSecret := fmt.Sprintf("SECRET-WORKER-%d-TOKEN", i)
		if s.Secret != ownSecret {
			fmt.Fprintf(os.Stderr, "LEAK: session %d secret=%q, want %q\n", i, s.Secret, ownSecret)
			leaks++
			continue
		}

		// No other worker's secrets may appear.
		for _, tok := range s.Tokens {
			if tok != ownSecret {
				fmt.Fprintf(os.Stderr, "LEAK: session %d contains foreign token %q\n", i, tok)
				leaks++
			}
		}
	}

	if leaks > 0 {
		fail("%d isolation breaches detected", leaks)
	}

	fmt.Printf("PASS: %d sessions verified — zero cross-session leakage\n", workers)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
