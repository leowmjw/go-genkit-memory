// Scenario 15: Nested Markdown Injection Exploits
//
// Verifies that user content containing nested markdown fences, raw Mermaid
// blocks, and unclosed code tags is treated as literal strings — it does not
// corrupt the adapter's own structured output or break session state.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario15_markdown_injection
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type InjectionState struct {
	LastInput string `json:"last_input"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[InjectionState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[InjectionState](store)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[InjectionState]("injection-session"),
		session.WithInitialState(InjectionState{}),
		session.WithStore[InjectionState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	injections := []string{
		// Triple-backtick fence inside user content
		"Here is code:\n```go\nfunc main() {\n```\nand more text",
		// Nested Mermaid block
		"```mermaid\ngraph LR\n  A-->B\n```\nThis is a mermaid diagram inside user text",
		// Unclosed code fence
		"Unclosed block:\n```python\nimport os\nprint(os.listdir())",
		// HTML injection attempt
		"<script>alert('xss')</script> and <b>bold</b> tags",
		// Mixed fences
		"````\nnested ``` fences ``` here\n````",
	}

	for i, payload := range injections {
		if err := adapter.Capture(ctx, "injection-session",
			payload,
			"Acknowledged your message."); err != nil {
			fmt.Printf("  injection %d capture warning: %v\n", i+1, err)
		}

		state := InjectionState{LastInput: payload}
		if err := sess.UpdateState(ctx, state); err != nil {
			fail("state update failed on injection %d: %v", i+1, err)
		}
	}

	// Reload — if any injection corrupted serialization this will fail.
	loaded, err := session.Load(ctx, adapter, "injection-session")
	if err != nil {
		fail("load session after injections: %v", err)
	}

	// Last state must equal the last injection payload exactly.
	last := injections[len(injections)-1]
	if loaded.State().LastInput != last {
		fail("state corrupted by injection: got %q, want %q",
			loaded.State().LastInput, last)
	}

	fmt.Printf("PASS: %d markdown injection payloads processed, session state intact\n",
		len(injections))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
