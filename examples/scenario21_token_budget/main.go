// Scenario 21: Deep Memory Context Starvation & LLM Window Overflow Fallbacks
//
// Verifies that when the gateway returns a massive recall context that would
// overflow the LLM token window, the adapter's token budget trimmer truncates
// it to a safe size before it reaches the generation layer.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario21_token_budget
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type BudgetState struct {
	LastRecall string `json:"last_recall"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	// Gateway that returns a 200 KB recall context — way above any token budget.
	const hugeContextBytes = 200 * 1024
	hugeContext := strings.Repeat("historical context sentence. ", hugeContextBytes/30)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/recall" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"context": hugeContext})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store, err := sqlitestore.NewStore[BudgetState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	// Token budget: 4096 tokens ≈ ~16 KB of text (4 chars/token heuristic).
	const maxTokens = 4096
	adapter := memstore.NewAdapter[BudgetState](store,
		memstore.WithGatewayURL(srv.URL),
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
