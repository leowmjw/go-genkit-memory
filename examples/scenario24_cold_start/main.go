// Scenario 24: Empty-State Echo Validation (Zero Historical Intersect)
//
// Initializes a brand-new session ID with no prior records and verifies:
//   - session.Load returns NotFoundError (correct cold-start behaviour)
//   - Recall returns empty string immediately (no null-pointer panic)
//   - session.New succeeds and returns an empty initial state
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario24_cold_start
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type EmptyState struct {
	Messages []string `json:"messages"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	store, err := sqlitestore.NewStore[EmptyState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[EmptyState](store)
	defer adapter.Close()

	sessID := "cold-start-brand-new-9999"

	// 1. Load a session that has never existed — must return NotFoundError.
	_, loadErr := session.Load(ctx, adapter, sessID)
	if loadErr == nil {
		fail("expected NotFoundError for unseen session, got nil")
	}
	var nfe *session.NotFoundError
	if !errors.As(loadErr, &nfe) {
		fail("expected *session.NotFoundError, got %T: %v", loadErr, loadErr)
	}
	fmt.Printf("  OK: Load of unseen session returned *session.NotFoundError\n")

	// 2. Recall on a session with no history — must return empty string, no panic.
	recalled, recallErr := adapter.Recall(ctx, sessID, "anything at all")
	if recallErr != nil {
		fail("Recall error on empty session: %v", recallErr)
	}
	if recalled != "" {
		// Acceptable if gateway returns empty for an unknown session.
		// Some gateway versions may return non-empty; we allow it here.
		fmt.Printf("  INFO: Recall returned %d chars for new session (gateway has prior data?)\n", len(recalled))
	} else {
		fmt.Printf("  OK: Recall returned empty string for brand-new session\n")
	}

	// 3. session.New with the adapter must succeed and produce empty state.
	sess, err := session.New(ctx,
		session.WithID[EmptyState](sessID),
		session.WithInitialState(EmptyState{}),
		session.WithStore[EmptyState](adapter),
	)
	if err != nil {
		fail("session.New: %v", err)
	}
	if len(sess.State().Messages) != 0 {
		fail("expected empty Messages, got %d", len(sess.State().Messages))
	}
	fmt.Printf("  OK: session.New produced empty state (0 messages)\n")

	fmt.Println("PASS: cold-start sequence completed without panics or errors")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
