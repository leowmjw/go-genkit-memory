package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestL1Deduplicator_NoCandidatesStores(t *testing.T) {
	store := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	dedup := NewL1Deduplicator(cfg, store, nil)

	memories := []MemoryRecord{
		{ID: "m1", Content: "user prefers Go", Type: MemoryTypePersona, Priority: 80, CreatedAt: time.Now()},
	}
	embeddings := [][]float32{{0.1, 0.2, 0.3}}

	decisions, err := dedup.Deduplicate(context.Background(), memories, embeddings)
	if err != nil {
		t.Fatalf("Deduplicate: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("want 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != DedupStore {
		t.Errorf("Action = %q, want store (no candidates)", decisions[0].Action)
	}
}

func TestL1Deduplicator_WithCandidatesCallsLLM(t *testing.T) {
	store := NewInMemoryStore()
	// Pre-populate store with an existing memory.
	existing := MemoryRecord{
		ID: "existing-1", Content: "user likes Go", Type: MemoryTypePersona,
		Priority: 70, CreatedAt: time.Now(),
	}
	_ = store.UpsertL1(existing, []float32{0.1, 0.2, 0.3})

	// Mock LLM to return skip decision.
	orig := callLLMDedup
	callLLMDedup = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		dec := DedupDecision{Action: DedupSkip, ExistingID: "existing-1", Reason: "duplicate"}
		b, _ := json.Marshal(dec)
		return string(b), nil
	}
	t.Cleanup(func() { callLLMDedup = orig })

	cfg := DefaultPipelineConfig()
	dedup := NewL1Deduplicator(cfg, store, nil)

	memories := []MemoryRecord{
		{ID: "new-1", Content: "user prefers Go", Type: MemoryTypePersona, Priority: 80, CreatedAt: time.Now()},
	}
	embeddings := [][]float32{{0.1, 0.2, 0.3}}

	decisions, err := dedup.Deduplicate(context.Background(), memories, embeddings)
	if err != nil {
		t.Fatalf("Deduplicate: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("want 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != DedupSkip {
		t.Errorf("Action = %q, want skip", decisions[0].Action)
	}
}

func TestL1Deduplicator_ApplyDecisions(t *testing.T) {
	store := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	dedup := NewL1Deduplicator(cfg, store, nil)

	memories := []MemoryRecord{
		{ID: "m1", Content: "fact one", Type: MemoryTypeEpisodic, Priority: 50, CreatedAt: time.Now()},
		{ID: "m2", Content: "fact two", Type: MemoryTypePersona, Priority: 60, CreatedAt: time.Now()},
	}
	decisions := []DedupDecision{
		{Action: DedupStore},
		{Action: DedupSkip, ExistingID: "old-1"},
	}
	embeddings := [][]float32{{0.1, 0.2}, {0.3, 0.4}}

	err := dedup.ApplyDecisions(context.Background(), memories, decisions, embeddings)
	if err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}

	// Only m1 should be stored (m2 was skipped).
	results, _ := store.SearchL1FTS("fact one", 10)
	if len(results) != 1 {
		t.Errorf("want 1 stored memory, got %d", len(results))
	}
}
