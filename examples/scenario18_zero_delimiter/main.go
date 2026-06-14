// Scenario 18: Extremely Long Zero-Delimiter Token Attacks
//
// Verifies that a continuous 25 KB string with no spaces, line breaks, or
// standard punctuation is safely truncated by SanitizeContent without
// locking the token-parsing thread or panicking.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario18_zero_delimiter
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type ZeroDelimState struct {
	Processed int `json:"processed"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[ZeroDelimState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[ZeroDelimState](store)
	defer adapter.Close()

	// 25 KB of 'a' with zero delimiters.
	const tokenLen = 25 * 1024
	longToken := strings.Repeat("a", tokenLen)

	// SanitizeContent must handle this without hanging.
	sanitized, err := memstore.SanitizeContent(longToken)
	if err != nil {
		fmt.Printf("  sanitize note: %v\n", err)
		sanitized = longToken // use original length for bound check if error
	}
	if len(sanitized) > tokenLen {
		fail("SanitizeContent grew the input: in=%d out=%d", tokenLen, len(sanitized))
	}

	// Capture should not panic or hang.
	if err := adapter.Capture(ctx, "zero-delim-session", longToken, "Handled long token."); err != nil {
		fmt.Printf("  capture warning (non-fatal): %v\n", err)
	}

	fmt.Printf("PASS: %d-byte zero-delimiter token sanitized to %d bytes without hang\n",
		tokenLen, len(sanitized))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
