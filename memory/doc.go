// Package memory provides a TencentDB-Agent-Memory adapter for genkit-go.
//
// It implements [session.Store] and connects to the TencentDB memory gateway
// sidecar (default: 127.0.0.1:8420) to provide a four-tier memory pipeline:
//
//   - L0: raw conversation capture (async, non-blocking)
//   - L1: episodic atom extraction (gateway-side, every N turns)
//   - L2: scene block aggregation (gateway-side)
//   - L3: persona synthesis (gateway-side, every N memories)
//
// The adapter wraps any existing [session.Store] (in-memory, BBolt, SQLite)
// for durable state storage and layers the gateway on top for long-term memory.
//
// # Quick start
//
//	disk, _ := sqlitestore.NewStore[MyState](ctx, "sessions.db")
//	adapter := memory.NewAdapter[MyState](disk)
//	defer adapter.Close()
//
//	sess, _ := session.New(ctx,
//	    session.WithStore[MyState](adapter),
//	    session.WithInitialState(MyState{}),
//	)
//
//	// Before each LLM call: pull long-term context.
//	ctx, _ := adapter.Recall(ctx, sessID, "current task query")
//
//	// After each conversation turn: capture for future recall.
//	adapter.Capture(ctx, sessID, userMsg, assistantMsg)
package memory
