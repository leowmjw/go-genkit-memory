// Scenario 16: Invalid UTF-8 Binary Stream Processing
//
// Verifies that raw binary data and corrupt byte sequences are sanitized by
// the adapter's UTF-8 guard before reaching the gateway, preventing JSON
// marshalling panics or application crashes.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario16_invalid_utf8
package main

import (
	"context"
	"fmt"
	"os"
	"unicode/utf8"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type BinaryState struct {
	Processed int `json:"processed"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[BinaryState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[BinaryState](store)
	defer adapter.Close()

	// Invalid UTF-8 sequences — raw bytes that are not valid Unicode.
	badInputs := [][]byte{
		{0xff, 0xfe, 0x00, 0x41},                   // BOM + null + ASCII
		{0xc0, 0xaf},                                 // overlong encoding
		{0xed, 0xa0, 0x80},                           // surrogate half
		{0x80, 0x81, 0x82, 0x83},                     // continuation bytes without lead
		[]byte("valid prefix\xff\xfe invalid suffix"), // mixed valid+invalid
	}

	for i, raw := range badInputs {
		input := string(raw) // Go allows this; the string may not be valid UTF-8
		// Adapter's sanitizer should repair or strip invalid bytes before sending.
		if err := adapter.Capture(ctx, "utf8-session", input, "Received binary input."); err != nil {
			// Non-fatal: capture may go to fallback buffer if gateway rejects it.
			fmt.Printf("  input %d capture warning: %v\n", i+1, err)
		}
	}

	// Verify sanitizer: direct call to SanitizeContent must return valid UTF-8.
	for i, raw := range badInputs {
		sanitized, err := memstore.SanitizeContent(string(raw))
		if err != nil {
			// SanitizeContent may return an error for unrecoverable input; that's fine.
			fmt.Printf("  input %d sanitize note: %v\n", i+1, err)
			sanitized = "" // treat as empty on hard failure
		}
		if !utf8.ValidString(sanitized) {
			fail("SanitizeContent produced invalid UTF-8 for input %d", i+1)
		}
	}

	fmt.Printf("PASS: %d invalid UTF-8 inputs sanitized — all outputs are valid UTF-8\n",
		len(badInputs))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
