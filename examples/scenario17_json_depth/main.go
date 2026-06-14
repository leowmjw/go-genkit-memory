// Scenario 17: Deep JSON Nesting Attack Vectors
//
// Verifies that deeply nested JSON payloads (250+ levels) are detected and
// rejected by CheckJSONDepth before reaching the marshal path, preventing
// stack exhaustion.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario17_json_depth
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type DepthState struct {
	Checked int `json:"checked"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[DepthState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[DepthState](store)
	defer adapter.Close()

	// Build a 300-level deep JSON object: {"a":{"a":{"a":...}}}
	deepJSON := strings.Repeat(`{"a":`, 300) + `"leaf"` + strings.Repeat(`}`, 300)

	// CheckJSONDepth should report the nesting exceeds the safe limit (100).
	if err := memstore.CheckJSONDepth([]byte(deepJSON), 100); err == nil {
		fail("CheckJSONDepth accepted 300-deep JSON — should have rejected")
	} else {
		fmt.Printf("  CheckJSONDepth correctly rejected 300-deep JSON: %v\n", err)
	}

	// Shallow JSON must still be accepted.
	shallowJSON := `{"key":"value","nested":{"inner":"data"}}`
	if err := memstore.CheckJSONDepth([]byte(shallowJSON), 100); err != nil {
		fail("CheckJSONDepth rejected valid shallow JSON: %v", err)
	}

	// Sending the deep JSON as message content should be handled without panic.
	captureErr := adapter.Capture(ctx, "depth-session", deepJSON, "Processed your input.")
	// Non-fatal — the adapter may sanitize or reject the payload.
	if captureErr != nil {
		fmt.Printf("  deep JSON capture warning (expected): %v\n", captureErr)
	}

	fmt.Printf("PASS: 300-deep JSON rejected, shallow JSON (depth≤3) accepted — no stack overflow\n")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
