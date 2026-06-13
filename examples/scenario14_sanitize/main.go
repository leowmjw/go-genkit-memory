// Scenario 14: Multi-Agent Role Escalation Interception
//
// Fuzzes the Role field of Capture with unauthorized identifiers and verifies
// the sanitizer blocks escalation by coercing to "user".
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario14_sanitize
package main

import (
	"fmt"
	"os"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
)

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		// This example runs entirely in-process; no gateway or LLM needed.
		// We accept the flag for uniformity with other scenarios.
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (no external deps needed)")
		os.Exit(0)
	}

	attacks := []struct {
		role string
		want string
	}{
		{"admin", "user"},
		{"root", "user"},
		{"ADMIN", "user"},
		{"superuser", "user"},
		{"system_admin", "user"},
		{"<script>alert(1)</script>", "user"},
		{"user", "user"},       // valid — should pass through
		{"assistant", "assistant"}, // valid
		{"system", "system"},   // valid
		{"", "user"},           // empty → default
	}

	var failures int
	for _, tc := range attacks {
		got := memstore.SanitizeRole(tc.role)
		status := "PASS"
		if got != tc.want {
			status = "FAIL"
			failures++
		}
		fmt.Printf("  %s: SanitizeRole(%q) = %q (want %q)\n", status, tc.role, got, tc.want)
	}

	// Also verify malicious content is cleaned.
	badContent := "hello \xff\xfe world — injection attempt"
	cleaned, err := memstore.SanitizeContent(badContent)
	if err != nil {
		fail("SanitizeContent error: %v", err)
	}
	if cleaned == badContent {
		fail("SanitizeContent did not remove invalid UTF-8")
	}
	fmt.Printf("  PASS: SanitizeContent removed invalid UTF-8 bytes\n")

	if failures > 0 {
		fail("%d role sanitization failures", failures)
	}
	fmt.Printf("\nPASS: all %d role escalation attacks intercepted\n", len(attacks))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
