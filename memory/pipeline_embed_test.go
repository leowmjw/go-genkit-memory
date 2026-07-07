package memory

import (
	"context"
	"testing"
)

func TestEmbeddingService_OpenAI_Stub(t *testing.T) {
	// Replace embedBatch with a stub that returns fake embeddings.
	orig := embedBatch
	embedBatch = func(_ context.Context, cfg EmbeddingConfig, texts []string) ([][]float32, error) {
		if cfg.Provider != EmbeddingProviderOpenAI {
			t.Errorf("Provider = %q, want openai", cfg.Provider)
		}
		result := make([][]float32, len(texts))
		for i := range texts {
			result[i] = []float32{0.1, 0.2, 0.3}
		}
		return result, nil
	}
	t.Cleanup(func() { embedBatch = orig })

	cfg := EmbeddingConfig{
		Provider:   EmbeddingProviderOpenAI,
		BaseURL:    "http://localhost:11434/v1",
		Model:      "text-embedding-3-small",
		Dimensions: 1536,
	}
	svc := NewEmbeddingService(cfg, nil)

	embeddings, err := svc.Embed(context.Background(), []string{"hello world", "test input"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("want 2 embeddings, got %d", len(embeddings))
	}
	if len(embeddings[0]) != 3 {
		t.Errorf("want 3 dims, got %d", len(embeddings[0]))
	}
}

func TestEmbeddingService_ONNX_Stub(t *testing.T) {
	cfg := EmbeddingConfig{
		Provider:      EmbeddingProviderONNX,
		ONNXModelPath: "/tmp/fake-model.onnx",
		Dimensions:    384,
	}
	svc := NewEmbeddingService(cfg, nil)

	embeddings, err := svc.Embed(context.Background(), []string{"test"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(embeddings) != 1 {
		t.Fatalf("want 1 embedding, got %d", len(embeddings))
	}
	if len(embeddings[0]) != 384 {
		t.Errorf("want 384 dims, got %d", len(embeddings[0]))
	}
}

func TestEmbeddingService_EmptyInput(t *testing.T) {
	cfg := EmbeddingConfig{Provider: EmbeddingProviderOpenAI}
	svc := NewEmbeddingService(cfg, nil)

	embeddings, err := svc.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if embeddings != nil {
		t.Errorf("want nil for empty input, got %v", embeddings)
	}
}

func TestEmbeddingService_FunctionSeam(t *testing.T) {
	var called bool
	orig := embedBatch
	embedBatch = func(_ context.Context, _ EmbeddingConfig, texts []string) ([][]float32, error) {
		called = true
		return make([][]float32, len(texts)), nil
	}
	t.Cleanup(func() { embedBatch = orig })

	cfg := EmbeddingConfig{Provider: EmbeddingProviderOpenAI, BaseURL: "http://x"}
	svc := NewEmbeddingService(cfg, nil)
	_, _ = svc.Embed(context.Background(), []string{"x"})

	if !called {
		t.Error("embedBatch function seam was not called")
	}
}
