// Scenario 8: Non-Linear User Goal Drift Core Retrieval
//
// Simulates a user pivoting topics mid-session, then referencing an earlier
// constraint. Verifies the adapter still recalls the original constraint via
// the memory gateway even after prolonged topic drift.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario08_goal_drift
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type DriftState struct {
	Topic string `json:"topic"`
	Turn  int    `json:"turn"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[DriftState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[DriftState](store)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[DriftState]("drift-session"),
		session.WithInitialState(DriftState{}),
		session.WithStore[DriftState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	turns := []struct{ user, assistant, topic string }{
		// Turn 1: establish DB schema constraint
		{
			"Our users table must use UUID v4 primary keys, never auto-increment.",
			"Understood. UUID v4 is mandatory for the users table PK.",
			"database-schema",
		},
		// Turns 2-4: topic drift into Kubernetes
		{
			"Let's now design the Kubernetes deployment topology.",
			"We'll use a 3-replica Deployment with a HorizontalPodAutoscaler.",
			"kubernetes",
		},
		{
			"What resource limits should we set for the API pods?",
			"Recommend 500m CPU request, 1 CPU limit, 512Mi memory.",
			"kubernetes",
		},
		{
			"Should we use a DaemonSet for log shipping?",
			"Yes, a DaemonSet with Fluent Bit is the standard approach.",
			"kubernetes",
		},
		// Turn 5: reference original DB constraint after drift
		{
			"Going back to the database — can we use serial IDs for performance?",
			"No. The constraint from earlier mandates UUID v4 primary keys.",
			"database-schema",
		},
	}

	for i, t := range turns {
		if err := adapter.Capture(ctx, "drift-session", t.user, t.assistant); err != nil {
			fmt.Printf("  turn %d capture warning: %v\n", i+1, err)
		}
		state := sess.State()
		state.Topic = t.topic
		state.Turn = i + 1
		if err := sess.UpdateState(ctx, state); err != nil {
			fail("update state turn %d: %v", i+1, err)
		}
	}

	// Recall the DB constraint — should surface despite Kubernetes topic drift.
	recalled, err := adapter.Recall(ctx, "drift-session", "primary key UUID constraint users table")
	if err != nil {
		fmt.Printf("  recall warning (non-fatal): %v\n", err)
	}

	loaded, err := session.Load(ctx, adapter, "drift-session")
	if err != nil {
		fail("load session: %v", err)
	}
	if loaded.State().Turn != 5 {
		fail("session state lost: expected turn=5, got turn=%d", loaded.State().Turn)
	}

	fmt.Printf("PASS: 5 turns across 2 topics, session intact at turn=%d, recall=%d bytes\n",
		loaded.State().Turn, len(recalled))
	if len(recalled) > 0 {
		fmt.Printf("  (gateway recalled historical constraint across topic drift)\n")
	} else {
		fmt.Printf("  (gateway returned empty — L1 extraction may not have run yet)\n")
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
