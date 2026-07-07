// Scenario 19: Cold-Start Recall Fast Path
//
// Verifies that Recall on a brand-new session with no historical data returns
// quickly and gracefully with an empty string.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario19_timeout
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	dataDir := filepath.Join("examples", "scenario19_timeout", ".memory")
	if err := os.RemoveAll(dataDir); err != nil {
		fail("reset data dir: %v", err)
	}
	defer os.RemoveAll(dataDir)

	ctx := context.Background()
	store, err := sqlitestore.NewStore[struct{}](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	cfg := memstore.DefaultPipelineConfig()
	cfg.DataDir = dataDir

	adapter := memstore.NewAdapter[struct{}](store,
		memstore.WithPipelineConfig(cfg),
		memstore.WithMemoryStore(memstore.NewInMemoryStore()),
	)
	defer adapter.Close()

	start := time.Now()
	text, err := adapter.Recall(ctx, "session-1", "")
	elapsed := time.Since(start)

	if err != nil {
		fail("Recall returned error: %v", err)
	}
	if text != "" {
		fail("Recall returned non-empty text for cold start: %q", text)
	}
	if elapsed > time.Second {
		fail("Recall took too long for cold start: %v", elapsed)
	}

	fmt.Printf("PASS: cold-start Recall returned empty string in %v\n", elapsed.Round(time.Millisecond))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
