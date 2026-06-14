// Scenario 11: Canvas Node Property Injection Collisions
//
// Verifies that concurrent goroutines writing distinct property keys to the
// same session do not produce data loss or race conditions. Each goroutine
// writes its own key; the final state must contain all keys.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario11_node_collisions
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

type NodeState struct {
	Properties map[string]string `json:"properties"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[NodeState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[NodeState](store)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[NodeState]("collision-session"),
		session.WithInitialState(NodeState{Properties: map[string]string{}}),
		session.WithStore[NodeState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// 10 goroutines each writing a distinct key.
	const workers = 10
	keys := make([]string, workers)
	for i := range workers {
		keys[i] = fmt.Sprintf("prop_%02d", i)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var writeErr error

	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := keys[idx]
			val := fmt.Sprintf("value_%02d", idx)

			mu.Lock()
			state := sess.State()
			if state.Properties == nil {
				state.Properties = make(map[string]string)
			}
			state.Properties[key] = val
			if err := sess.UpdateState(ctx, state); err != nil {
				writeErr = err
			}
			mu.Unlock()

			// Capture is fire-and-forget; errors are non-fatal here.
			_ = adapter.Capture(ctx, "collision-session",
				fmt.Sprintf("Set property %s", key),
				fmt.Sprintf("Property %s set to %s", key, val))
		}(i)
	}
	wg.Wait()

	if writeErr != nil {
		fail("concurrent write error: %v", writeErr)
	}

	loaded, err := session.Load(ctx, adapter, "collision-session")
	if err != nil {
		fail("load session: %v", err)
	}

	props := loaded.State().Properties
	for _, k := range keys {
		if _, ok := props[k]; !ok {
			fail("property %q missing after concurrent writes", k)
		}
	}

	fmt.Printf("PASS: %d concurrent property writes, all %d keys intact\n",
		workers, len(props))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
