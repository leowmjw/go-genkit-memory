// Scenario 4: Graceful Degradation Without an LLM Endpoint
//
// Runs the local in-process pipeline with no LLM or embedding endpoint
// configured and verifies: Capture still succeeds, Recall degrades to empty
// context, and the session loop continues without panics.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario04_degradation
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

type ChatState struct {
	Messages []string `json:"messages"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	dataDir := filepath.Join("examples", "scenario04_degradation", ".memory")
	if err := os.RemoveAll(dataDir); err != nil {
		fail("reset data dir: %v", err)
	}
	defer os.RemoveAll(dataDir)

	store, err := sqlitestore.NewStore[ChatState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	cfg := memstore.DefaultPipelineConfig()
	cfg.DataDir = dataDir
	cfg.LLM = memstore.LLMConfig{}
	cfg.Embedding = memstore.EmbeddingConfig{}

	memStore := memstore.NewInMemoryStore()
	adapter := memstore.NewAdapter[ChatState](store,
		memstore.WithPipelineConfig(cfg),
		memstore.WithMemoryStore(memStore),
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

	// Run 5 turns with no live LLM endpoint available.
	for i := range 5 {
		userMsg := fmt.Sprintf("turn %d question", i+1)
		assistMsg := fmt.Sprintf("turn %d answer (degraded mode)", i+1)

		if err := adapter.Capture(ctx, sessID, userMsg, assistMsg); err != nil {
			fail("turn %d: Capture returned error: %v", i+1, err)
		}

		// Recall must not error even when no embedding endpoint is configured.
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
	}

	if buffered := adapter.FallbackLen(); buffered != 0 {
		fail("expected no fallback buffering for local graceful degradation, got %d", buffered)
	}

	finalMsgs := sess.State().Messages
	if len(finalMsgs) != 5 {
		fail("expected 5 messages, got %d", len(finalMsgs))
	}

	fmt.Printf("PASS: %d turns completed with local graceful degradation and empty recall context\n",
		len(finalMsgs))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
