// Scenario 19: Slow-Loris Network Stream Emulation (Timeout Boundary)
//
// Points the adapter at a locally spawned slow HTTP server that takes > 6 s
// to respond. Verifies:
//   - Recall returns within the 5 s gateway timeout
//   - The caller gets empty string (graceful degradation), not an error
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario19_timeout
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	// Start a local slow server on a random port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("listen: %v", err)
	}
	slowAddr := ln.Addr().String()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Delay longer than the 5 s gateway timeout.
			select {
			case <-time.After(8 * time.Second):
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(memstore.RecallResponse{Context: "too late"})
		}),
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	ctx := context.Background()
	store, err := sqlitestore.NewStore[struct{}](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[struct{}](store,
		memstore.WithGatewayURL("http://"+slowAddr),
	)
	defer adapter.Close()

	start := time.Now()
	text, err := adapter.Recall(ctx, "session-1", "anything")
	elapsed := time.Since(start)

	if err != nil {
		fail("Recall returned error (should degrade gracefully): %v", err)
	}
	if text != "" {
		fail("Recall returned non-empty text despite timeout: %q", text)
	}
	if elapsed > 6*time.Second {
		fail("Recall took too long: %v (timeout should fire at 5 s)", elapsed)
	}

	fmt.Printf("PASS: Recall returned empty string in %v (< 6 s timeout boundary)\n", elapsed.Round(time.Millisecond))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
