// Package memory provides a 4-tier semantic memory pipeline for genkit-go.
//
// It implements [session.Store] and runs an in-process L0→L3 pipeline using
// an OpenAI-compatible LLM endpoint for semantic processing:
//
//   - L0: raw conversation capture (append-only JSONL, permissive)
//   - L1: episodic atom extraction + deduplication (LLM-powered)
//   - L2: scene block aggregation (LLM-powered)
//   - L3: persona synthesis (LLM-powered, ≤2000 chars)
//
// The adapter wraps any existing [session.Store] (in-memory, BBolt, SQLite)
// for durable state storage and layers the pipeline on top for long-term memory.
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
