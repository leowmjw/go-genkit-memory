package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// callLLMDedup is a function variable seam for testing.
// It calls the LLM for conflict detection between new and existing memories.
var callLLMDedup = func(ctx context.Context, cfg LLMConfig, systemPrompt, userPrompt string) (string, error) {
	return callChatCompletion(ctx, cfg, systemPrompt, userPrompt)
}

// searchVectors is a function variable seam for testing.
// It searches for similar memories by vector embedding.
var searchVectors = func(ctx context.Context, store MemoryStore, query []float32, topK int) ([]L1SearchResult, error) {
	// Stub: delegates to the MemoryStore implementation.
	_ = ctx
	return store.SearchL1Vector(query, topK)
}

// L1Deduplicator resolves conflicts between new and existing memories.
type L1Deduplicator struct {
	cfg   PipelineConfig
	store MemoryStore
	log   *slog.Logger
}

// NewL1Deduplicator creates a new deduplicator.
func NewL1Deduplicator(cfg PipelineConfig, store MemoryStore, log *slog.Logger) *L1Deduplicator {
	if log == nil {
		log = slog.Default()
	}
	return &L1Deduplicator{cfg: cfg, store: store, log: log}
}

// Deduplicate checks new memories against existing ones and returns dedup decisions.
// For each new memory, it:
// 1. Searches for similar existing memories via vector similarity
// 2. Falls back to FTS/BM25 keyword search
// 3. Calls LLM for conflict resolution if candidates are found
func (d *L1Deduplicator) Deduplicate(ctx context.Context, newMemories []MemoryRecord, embeddings [][]float32) ([]DedupDecision, error) {
	if len(newMemories) == 0 {
		return nil, nil
	}

	decisions := make([]DedupDecision, len(newMemories))

	for i, mem := range newMemories {
		var candidates []L1SearchResult

		// Vector search if we have an embedding.
		if i < len(embeddings) && len(embeddings[i]) > 0 {
			results, err := searchVectors(ctx, d.store, embeddings[i], 5)
			if err != nil {
				d.log.Warn("vector search failed, falling back to FTS",
					slog.String("err", err.Error()))
			} else {
				candidates = append(candidates, results...)
			}
		}

		// FTS fallback.
		if len(candidates) == 0 {
			ftsResults, err := d.store.SearchL1FTS(mem.Content, 5)
			if err != nil {
				d.log.Warn("FTS search failed", slog.String("err", err.Error()))
			} else {
				candidates = append(candidates, ftsResults...)
			}
		}

		// No candidates → store directly.
		if len(candidates) == 0 {
			decisions[i] = DedupDecision{Action: DedupStore}
			continue
		}

		// Call LLM for conflict resolution.
		decision, err := d.resolveConflict(ctx, mem, candidates)
		if err != nil {
			d.log.Warn("conflict resolution failed, defaulting to store",
				slog.String("err", err.Error()))
			decisions[i] = DedupDecision{Action: DedupStore}
			continue
		}
		decisions[i] = decision
	}

	return decisions, nil
}

// ApplyDecisions applies dedup decisions to the memory store.
func (d *L1Deduplicator) ApplyDecisions(ctx context.Context, memories []MemoryRecord, decisions []DedupDecision, embeddings [][]float32) error {
	_ = ctx
	for i, dec := range decisions {
		if i >= len(memories) {
			break
		}
		mem := memories[i]
		var emb []float32
		if i < len(embeddings) {
			emb = embeddings[i]
		}

		switch dec.Action {
		case DedupStore:
			if err := d.store.UpsertL1(mem, emb); err != nil {
				return fmt.Errorf("pipeline_l1_dedup: store: %w", err)
			}
		case DedupUpdate:
			if dec.NewContent != "" {
				mem.Content = dec.NewContent
			}
			mem.ID = dec.ExistingID
			if err := d.store.UpsertL1(mem, emb); err != nil {
				return fmt.Errorf("pipeline_l1_dedup: update: %w", err)
			}
		case DedupMerge:
			if dec.NewContent != "" {
				mem.Content = dec.NewContent
			}
			mem.ID = dec.ExistingID
			if err := d.store.UpsertL1(mem, emb); err != nil {
				return fmt.Errorf("pipeline_l1_dedup: merge: %w", err)
			}
		case DedupSkip:
			d.log.Debug("skipping duplicate memory",
				slog.String("id", mem.ID),
				slog.String("existing", dec.ExistingID),
			)
		}
	}
	return nil
}

// resolveConflict calls the LLM to decide how to handle a conflict.
func (d *L1Deduplicator) resolveConflict(ctx context.Context, newMem MemoryRecord, candidates []L1SearchResult) (DedupDecision, error) {
	userPrompt := d.buildConflictPrompt(newMem, candidates)

	raw, err := callLLMDedup(ctx, d.cfg.LLM, l1DedupSystemPrompt, userPrompt)
	if err != nil {
		return DedupDecision{}, fmt.Errorf("llm dedup call: %w", err)
	}

	// Parse LLM decision.
	cleaned := stripJSONFences(raw)
	var decision DedupDecision
	if err := json.Unmarshal([]byte(cleaned), &decision); err != nil {
		return DedupDecision{Action: DedupStore}, nil // fallback to store on parse error
	}
	return decision, nil
}

// buildConflictPrompt formats the new memory and candidates for the LLM.
func (d *L1Deduplicator) buildConflictPrompt(newMem MemoryRecord, candidates []L1SearchResult) string {
	var b []byte
	b = append(b, fmt.Sprintf("New memory:\n  Content: %s\n  Type: %s\n\nExisting candidates:\n", newMem.Content, newMem.Type)...)
	for i, c := range candidates {
		b = append(b, fmt.Sprintf("  %d. [ID: %s] %s (type: %s, score: %.3f)\n",
			i+1, c.Record.ID, c.Record.Content, c.Record.Type, c.Score)...)
	}
	return string(b)
}
