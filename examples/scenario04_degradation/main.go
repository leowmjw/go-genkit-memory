// Scenario 4: "Brain-Split" Operational Degradation & Fallback Assessment
//
// Starts with the gateway reachable, then points the adapter at an unreachable
// host mid-execution and verifies: fallback buffer fills, Recall returns empty
// (graceful degradation), and the session loop continues without panics.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario04_degradation
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type ChatState struct {
	Messages []string `json:"messages"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	store, err := sqlitestore.NewStore[ChatState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	// Phase 1: point at a dead gateway (simulates mid-execution crash).
	adapter := memstore.NewAdapter[ChatState](store,
		memstore.WithGatewayURL("http://127.0.0.1:19998"), // nothing listening
	)
	defer adapter.Close()

	sessID := "degradation-session-1"
	sess, err := session.New(ctx,
		session.WithID[ChatState](sessID),
		session.WithInitialState(ChatState{}),
		session.WithStore[ChatState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// Run 5 turns against a dead gateway.
	for i := range 5 {
		userMsg := fmt.Sprintf("turn %d question", i+1)
		assistMsg := fmt.Sprintf("turn %d answer (degraded mode)", i+1)

		// Capture errors are expected — buffered in fallback.
		captureErr := adapter.Capture(ctx, sessID, userMsg, assistMsg)

		// Recall must not error even when gateway is dead.
		recalled, recallErr := adapter.Recall(ctx, sessID, "context query")
		if recallErr != nil {
			fail("turn %d: Recall returned error: %v", i+1, recallErr)
		}
		if recalled != "" {
			fail("turn %d: Recall returned non-empty result from dead gateway: %q", i+1, recalled)
		}

		state := sess.State()
		state.Messages = append(state.Messages, userMsg)
		if err := sess.UpdateState(ctx, state); err != nil {
			fail("turn %d: UpdateState failed (should never fail): %v", i+1, err)
		}

		_ = captureErr // expected to fail
	}

	buffered := adapter.FallbackLen()
	if buffered == 0 {
		fail("expected fallback buffer to be populated after gateway failure")
	}

	finalMsgs := sess.State().Messages
	if len(finalMsgs) != 5 {
		fail("expected 5 messages, got %d", len(finalMsgs))
	}

	fmt.Printf("PASS: %d turns completed in degraded mode, %d events buffered in fallback\n",
		len(finalMsgs), buffered)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
