package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// embedBatch is a function variable seam for testing.
// It produces embeddings for a batch of texts.
var embedBatch = func(ctx context.Context, cfg EmbeddingConfig, texts []string) ([][]float32, error) {
	switch cfg.Provider {
	case EmbeddingProviderONNX:
		return embedBatchONNX(ctx, cfg, texts)
	default:
		return embedBatchOpenAI(ctx, cfg, texts)
	}
}

// EmbeddingService produces vector embeddings for text inputs.
type EmbeddingService struct {
	cfg EmbeddingConfig
	log *slog.Logger
}

// NewEmbeddingService creates a new embedding service.
func NewEmbeddingService(cfg EmbeddingConfig, log *slog.Logger) *EmbeddingService {
	if log == nil {
		log = slog.Default()
	}
	return &EmbeddingService{cfg: cfg, log: log}
}

// Embed produces embeddings for the given texts.
func (s *EmbeddingService) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	start := time.Now()
	result, err := embedBatch(ctx, s.cfg, texts)
	if err != nil {
		return nil, err
	}
	s.log.Debug("embedding batch complete",
		slog.Int("count", len(texts)),
		slog.Duration("latency", time.Since(start)),
		slog.String("provider", string(s.cfg.Provider)),
	)
	return result, nil
}

// ─── OpenAI-compatible embedding API ─────────────────────────────────────────

// openAIEmbedRequest is the request body for the OpenAI embeddings endpoint.
type openAIEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// openAIEmbedResponse is the response from the OpenAI embeddings endpoint.
type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// embedBatchOpenAI calls an OpenAI-compatible embedding API.
func embedBatchOpenAI(ctx context.Context, cfg EmbeddingConfig, texts []string) ([][]float32, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("pipeline_embed: BaseURL is required for OpenAI provider")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	reqBody := openAIEmbedRequest{
		Model: cfg.Model,
		Input: texts,
	}
	if cfg.Dimensions > 0 {
		reqBody.Dimensions = cfg.Dimensions
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("pipeline_embed: marshal: %w", err)
	}

	httpCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost,
		cfg.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("pipeline_embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pipeline_embed: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("pipeline_embed: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("pipeline_embed: decode: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	return embeddings, nil
}

// ─── ONNX local embedding (stub) ────────────────────────────────────────────

// embedBatchONNX performs local in-process embedding using an ONNX model.
// TODO: Implement ONNX runtime integration in a future phase.
func embedBatchONNX(_ context.Context, cfg EmbeddingConfig, texts []string) ([][]float32, error) {
	if cfg.ONNXModelPath == "" {
		return nil, fmt.Errorf("pipeline_embed: ONNXModelPath is required for ONNX provider")
	}

	// Stub: return zero vectors of the configured dimension.
	// Real implementation will load the ONNX model and run inference.
	dims := cfg.Dimensions
	if dims == 0 {
		dims = 384 // common for small models
	}
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = make([]float32, dims)
	}
	return embeddings, nil
}
