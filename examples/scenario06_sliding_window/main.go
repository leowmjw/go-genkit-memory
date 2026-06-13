// Scenario 6: High-Frequency Turn Vaporization (Sliding Window Compaction)
//
// Fires 100 rapid-fire messages and verifies strict chronological ordering
// is preserved in the session state — no messages are dropped or reordered.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario06_sliding_window
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type StreamState struct {
	Messages []string `json:"messages"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	store, err := sqlitestore.NewStore[StreamState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[StreamState](store)
	defer adapter.Close()

	const total = 100
	sessID := "sliding-session-1"

	sess, err := session.New(ctx,
		session.WithID[StreamState](sessID),
		session.WithInitialState(StreamState{}),
		session.WithStore[StreamState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// Fire messages in rapid succession with minimal delay.
	for i := range total {
		msg := fmt.Sprintf("msg-%04d", i)
		state := sess.State()
		state.Messages = append(state.Messages, msg)
		if err := sess.UpdateState(ctx, state); err != nil {
			fail("UpdateState %d: %v", i, err)
		}
		_ = adapter.Capture(ctx, sessID, msg, fmt.Sprintf("response-%04d", i))
		// Minimal sleep to simulate rapid streaming (1 ms).
		time.Sleep(time.Millisecond)
	}

	// Reload and verify strict ordering.
	loaded, err := session.Load(ctx, adapter, sessID)
	if err != nil {
		fail("load: %v", err)
	}

	msgs := loaded.State().Messages
	if len(msgs) != total {
		fail("message count: want %d, got %d", total, len(msgs))
	}
	for i, m := range msgs {
		want := fmt.Sprintf("msg-%04d", i)
		if m != want {
			fail("ordering violation at index %d: want %q got %q", i, want, m)
		}
	}

	fmt.Printf("PASS: %d messages in strict chronological order\n", total)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
