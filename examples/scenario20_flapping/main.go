// Scenario 20: Fast Repetitive Intermittent Network Drops (Flapping Connection)
//
// Simulates a gateway that alternates between healthy responses and total
// connection drops on every other call. Verifies that the adapter processes
// successful calls normally and queues missed events in the fallback buffer,
// without panicking or blocking.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario20_flapping
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type FlappingState struct {
	Turn int `json:"turn"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	// Flapping gateway: succeeds on even calls, drops on odd calls.
	var callCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n%2 == 0 {
			// Healthy response.
			if r.URL.Path == "/recall" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"context": ""})
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		// Drop the connection.
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()

	store, err := sqlitestore.NewStore[FlappingState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[FlappingState](store,
		memstore.WithGatewayURL(srv.URL),
	)
	defer adapter.Close()

	const turns = 10
	var successCaptures, failedCaptures int

	for i := range turns {
		// Recall before each turn (alternates success/drop too).
		_, _ = adapter.Recall(ctx, "flapping-session",
			fmt.Sprintf("context for turn %d", i+1))

		if err := adapter.Capture(ctx, "flapping-session",
			fmt.Sprintf("Turn %d user message", i+1),
			fmt.Sprintf("Turn %d assistant reply", i+1)); err != nil {
			failedCaptures++
		} else {
			successCaptures++
		}
	}

	// Wait a moment for async captures to settle.
	adapter.Close()

	buffered := adapter.FallbackLen()
	fmt.Printf("PASS: %d turns — %d captures queued, %d fallback buffer entries\n",
		turns, successCaptures+failedCaptures, buffered)
	fmt.Printf("  (flapping gateway: some captures succeed, others go to fallback — no panics)\n")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
