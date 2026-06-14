// Scenario 12: Interleaved Cross-Talk Between Concurrent Agents
//
// Two agents (CodeGen and SecurityReviewer) write to the same session key
// simultaneously. Verifies that role labels and message content are never
// blended — each agent's messages remain attributable to their own role.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario12_crosstalk
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type AgentEntry struct {
	Agent   string `json:"agent"`
	Message string `json:"message"`
}

type SharedState struct {
	Log []AgentEntry `json:"log"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run (requires gateway + LLM)")
		os.Exit(0)
	}

	ctx := context.Background()

	store, err := sqlitestore.NewStore[SharedState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[SharedState](store)
	defer adapter.Close()

	sess, err := session.New(ctx,
		session.WithID[SharedState]("crosstalk-session"),
		session.WithInitialState(SharedState{}),
		session.WithStore[SharedState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	agents := []struct {
		name    string
		user    string
		assistant string
	}{
		{"CodeGen", "Generate a JWT auth function", "func GenerateJWT(claims) (string, error) { ... }"},
		{"SecurityReviewer", "Review the JWT auth function for vulnerabilities", "Found: weak default algo HS256; recommend RS256 with key rotation."},
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	captureErrs := make([]error, len(agents))

	for i, ag := range agents {
		wg.Add(1)
		go func(idx int, a struct {
			name, user, assistant string
		}) {
			defer wg.Done()

			captureErrs[idx] = adapter.Capture(ctx, "crosstalk-session", a.user, a.assistant)

			mu.Lock()
			state := sess.State()
			state.Log = append(state.Log, AgentEntry{Agent: a.name, Message: a.assistant})
			_ = sess.UpdateState(ctx, state)
			mu.Unlock()
		}(i, ag)
	}
	wg.Wait()

	for i, e := range captureErrs {
		if e != nil {
			fmt.Printf("  agent %s capture warning: %v\n", agents[i].name, e)
		}
	}

	loaded, err := session.Load(ctx, adapter, "crosstalk-session")
	if err != nil {
		fail("load session: %v", err)
	}

	log := loaded.State().Log
	if len(log) != 2 {
		fail("log entry count wrong: got %d, want 2", len(log))
	}

	// Verify no cross-contamination: CodeGen message must not appear in SecurityReviewer entry.
	for _, entry := range log {
		if entry.Agent == "SecurityReviewer" && strings.Contains(entry.Message, "GenerateJWT") {
			fail("cross-talk detected: SecurityReviewer entry contains CodeGen code")
		}
		if entry.Agent == "CodeGen" && strings.Contains(entry.Message, "HS256") {
			fail("cross-talk detected: CodeGen entry contains SecurityReviewer analysis")
		}
	}

	fmt.Printf("PASS: 2 agents wrote concurrently, %d log entries — no cross-talk detected\n",
		len(log))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
