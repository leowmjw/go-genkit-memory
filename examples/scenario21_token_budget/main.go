// Scenario 21: Deep Memory Context Token Budget Trimming
//
// Pre-populates the local memory store with a massive memory record and
// verifies the adapter trims the recalled context to the configured token
// budget before it reaches the generation layer.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario21_token_budget
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type BudgetState struct {
	LastRecall string `json:"last_recall"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	dataDir := filepath.Join("examples", "scenario21_token_budget", ".memory")
	if err := os.RemoveAll(dataDir); err != nil {
		fail("reset data dir: %v", err)
	}
	defer os.RemoveAll(dataDir)

	const hugeContextBytes = 200 * 1024
	hugeContext := strings.Repeat("project constraints historical context sentence. ", hugeContextBytes/47)

	store, err := sqlitestore.NewStore[BudgetState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	memStore := memstore.NewInMemoryStore()
	if err := memStore.UpsertL1(memstore.MemoryRecord{
		ID:        "huge-memory-1",
		Content:   hugeContext,
		Type:      memstore.MemoryTypeEpisodic,
		Priority:  95,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil); err != nil {
		fail("populate memory store: %v", err)
	}

	cfg := memstore.DefaultPipelineConfig()
	cfg.DataDir = dataDir
	cfg.Embedding = memstore.EmbeddingConfig{}

	// Token budget: 4096 tokens ≈ ~16 KB of text (4 chars/token heuristic).
	const maxTokens = 4096
	adapter := memstore.NewAdapter[BudgetState](store,
		memstore.WithPipelineConfig(cfg),
		memstore.WithMemoryStore(memStore),
		memstore.WithMaxRecallTokens(maxTokens),
	)
	defer adapter.Close()

	recalled, err := adapter.Recall(ctx, "budget-session", "project constraints")
	if err != nil {
		fail("recall error: %v", err)
	}

	// Approximate token count: len/4 (conservative).
	approxTokens := len(recalled) / 4
	if approxTokens > maxTokens {
		fail("recalled context exceeds token budget: ~%d tokens (max %d), len=%d bytes",
			approxTokens, maxTokens, len(recalled))
	}

	fmt.Printf("PASS: %d KB recall trimmed to ~%d tokens (%d bytes) — within %d-token budget\n",
		hugeContextBytes/1024, approxTokens, len(recalled), maxTokens)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
