package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPipelineManager_Capture(t *testing.T) {
	// Stub all LLM calls.
	origExtract := callLLMExtract
	callLLMExtract = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		segments := []SceneSegment{{
			SceneName: "test",
			Memories: []MemoryRecord{{
				Content:  "user likes Go",
				Type:     MemoryTypePersona,
				Priority: 80,
			}},
		}}
		b, _ := json.Marshal(segments)
		return string(b), nil
	}
	t.Cleanup(func() { callLLMExtract = origExtract })

	origEmbed := embedBatch
	embedBatch = func(_ context.Context, _ EmbeddingConfig, texts []string) ([][]float32, error) {
		result := make([][]float32, len(texts))
		for i := range texts {
			result[i] = []float32{0.1, 0.2, 0.3}
		}
		return result, nil
	}
	t.Cleanup(func() { embedBatch = origEmbed })

	origWrite := writeJSONL
	writeJSONL = func(_ string, _ []L0MessageRecord) error { return nil }
	t.Cleanup(func() { writeJSONL = origWrite })

	store := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()
	cfg.L1TriggerAfterTurns = 1

	pm := NewPipelineManager(cfg, store, nil)
	defer pm.Close()

	msgs := []ConversationMessage{
		{ID: "m1", Role: "user", Content: "I really like programming in Go.", Timestamp: time.Now()},
	}

	err := pm.Capture(context.Background(), "sess-1", msgs)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Verify L1 memory was stored.
	all, _ := store.GetAllL1()
	if len(all) != 1 {
		t.Errorf("want 1 L1 memory stored, got %d", len(all))
	}
}

func TestPipelineManager_Recall_Empty(t *testing.T) {
	origEmbed := embedBatch
	embedBatch = func(_ context.Context, _ EmbeddingConfig, texts []string) ([][]float32, error) {
		return make([][]float32, len(texts)), nil
	}
	t.Cleanup(func() { embedBatch = origEmbed })

	store := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	pm := NewPipelineManager(cfg, store, nil)
	defer pm.Close()

	// Empty store → empty recall.
	result, err := pm.Recall(context.Background(), "sess-1", "anything")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if result != "" {
		t.Errorf("want empty string for empty store, got %q", result)
	}
}

func TestPipelineManager_Recall_WithMemories(t *testing.T) {
	origEmbed := embedBatch
	embedBatch = func(_ context.Context, _ EmbeddingConfig, texts []string) ([][]float32, error) {
		result := make([][]float32, len(texts))
		for i := range texts {
			result[i] = []float32{1.0, 0.0, 0.0}
		}
		return result, nil
	}
	t.Cleanup(func() { embedBatch = origEmbed })

	store := NewInMemoryStore()
	_ = store.UpsertL1(MemoryRecord{
		ID: "m1", Content: "user prefers Go", Type: MemoryTypePersona,
		Priority: 80, CreatedAt: time.Now(),
	}, []float32{1.0, 0.0, 0.0})

	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	pm := NewPipelineManager(cfg, store, nil)
	defer pm.Close()

	result, err := pm.Recall(context.Background(), "sess-1", "programming language")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty recall result with memories in store")
	}
}

func TestPipelineManager_Close(t *testing.T) {
	store := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	pm := NewPipelineManager(cfg, store, nil)
	if err := pm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After close, Capture should fail.
	err := pm.Capture(context.Background(), "s1", []ConversationMessage{
		{ID: "m1", Role: "user", Content: "test", Timestamp: time.Now()},
	})
	if err == nil {
		t.Error("expected error after Close, got nil")
	}
}

func TestPipelineManager_CursorDedup(t *testing.T) {
	var writeCount int
	origWrite := writeJSONL
	writeJSONL = func(_ string, records []L0MessageRecord) error {
		writeCount += len(records)
		return nil
	}
	t.Cleanup(func() { writeJSONL = origWrite })

	origExtract := callLLMExtract
	callLLMExtract = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		return "[]", nil
	}
	t.Cleanup(func() { callLLMExtract = origExtract })

	origEmbed := embedBatch
	embedBatch = func(_ context.Context, _ EmbeddingConfig, texts []string) ([][]float32, error) {
		return make([][]float32, len(texts)), nil
	}
	t.Cleanup(func() { embedBatch = origEmbed })

	store := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	pm := NewPipelineManager(cfg, store, nil)
	defer pm.Close()

	t1 := time.Now().Add(-2 * time.Second)
	t2 := time.Now().Add(-1 * time.Second)

	msgs := []ConversationMessage{
		{ID: "m1", Role: "user", Content: "hello world message", Timestamp: t1},
		{ID: "m2", Role: "assistant", Content: "hi there response msg", Timestamp: t2},
	}

	ctx := context.Background()
	_ = pm.Capture(ctx, "s1", msgs)
	_ = pm.Capture(ctx, "s1", msgs) // same messages again

	if writeCount != 2 {
		t.Errorf("want 2 writes (deduped), got %d", writeCount)
	}
}
