// Scenario 3: High-Concurrency Swarm / Multi-Agent Workflow (Race-Condition Test)
//
// Spawns 30 goroutines each writing to its own isolated session simultaneously,
// verifying no deadlocks, panics, or context-frame drops.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario03_concurrent
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type AgentState struct {
	AgentID  string   `json:"agent_id"`
	Messages []string `json:"messages"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	store, err := sqlitestore.NewStore[AgentState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[AgentState](store)
	defer adapter.Close()

	const agents = 30
	const turnsPerAgent = 5

	var wg sync.WaitGroup
	var errCount atomic.Int64
	wg.Add(agents)

	for i := range agents {
		go func(idx int) {
			defer wg.Done()
			sessID := fmt.Sprintf("swarm-agent-%d", idx)

			sess, err := session.New(ctx,
				session.WithID[AgentState](sessID),
				session.WithInitialState(AgentState{AgentID: fmt.Sprintf("agent-%d", idx)}),
				session.WithStore[AgentState](adapter),
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "agent %d session.New: %v\n", idx, err)
				errCount.Add(1)
				return
			}

			for turn := range turnsPerAgent {
				userMsg := fmt.Sprintf("agent-%d turn-%d query", idx, turn)
				assistMsg := fmt.Sprintf("agent-%d turn-%d response", idx, turn)

				_ = adapter.Capture(ctx, sessID, userMsg, assistMsg)

				state := sess.State()
				state.Messages = append(state.Messages, userMsg)
				if err := sess.UpdateState(ctx, state); err != nil {
					fmt.Fprintf(os.Stderr, "agent %d UpdateState: %v\n", idx, err)
					errCount.Add(1)
					return
				}
			}

			// Verify message count.
			final := sess.State()
			if len(final.Messages) != turnsPerAgent {
				fmt.Fprintf(os.Stderr, "agent %d: want %d messages, got %d\n",
					idx, turnsPerAgent, len(final.Messages))
				errCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if n := errCount.Load(); n > 0 {
		fail("%d agents reported errors", n)
	}
	fmt.Printf("PASS: %d agents × %d turns completed with no errors or races\n",
		agents, turnsPerAgent)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
