package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
)

// PipelineManager orchestrates the L0→L1→L2→L3 pipeline.
// It schedules tier processing based on configurable thresholds.
type PipelineManager struct {
	cfg      PipelineConfig
	log      *slog.Logger
	store    MemoryStore
	embedSvc *EmbeddingService
	l0       *L0Recorder
	l1       *L1Extractor
	l1Dedup  *L1Deduplicator
	l2       *L2SceneExtractor
	l3       *L3PersonaGenerator

	// Counters for tier triggering.
	l0SinceL1 atomic.Int64
	l1SinceL2 atomic.Int64
	l2SinceL3 atomic.Int64

	// Shutdown control.
	mu       sync.Mutex
	shutdown bool
}

// NewPipelineManager creates and initializes the full pipeline.
func NewPipelineManager(cfg PipelineConfig, store MemoryStore, log *slog.Logger) *PipelineManager {
	if log == nil {
		log = slog.Default()
	}

	embedSvc := NewEmbeddingService(cfg.Embedding, log)

	return &PipelineManager{
		cfg:      cfg,
		log:      log,
		store:    store,
		embedSvc: embedSvc,
		l0:       NewL0Recorder(cfg.DataDir),
		l1:       NewL1Extractor(cfg, log),
		l1Dedup:  NewL1Deduplicator(cfg, store, log),
		l2:       NewL2SceneExtractor(cfg, store, log),
		l3:       NewL3PersonaGenerator(cfg, log),
	}
}

// Capture processes a conversation turn through the pipeline.
// L0 is always executed synchronously. Higher tiers are triggered based on thresholds.
func (pm *PipelineManager) Capture(ctx context.Context, sessionKey string, messages []ConversationMessage) error {
	pm.mu.Lock()
	if pm.shutdown {
		pm.mu.Unlock()
		return fmt.Errorf("pipeline_manager: shutting down")
	}
	pm.mu.Unlock()

	// L0: Record raw conversation.
	records, err := pm.l0.RecordConversation(ctx, sessionKey, messages)
	if err != nil {
		return fmt.Errorf("pipeline_manager: L0: %w", err)
	}

	if len(records) == 0 {
		return nil
	}

	pm.l0SinceL1.Add(int64(len(records)))

	// Check if L1 should trigger.
	if pm.l0SinceL1.Load() >= int64(pm.cfg.L1TriggerAfterTurns) {
		if err := pm.runL1(ctx, records); err != nil {
			pm.log.Warn("L1 extraction failed", slog.String("err", err.Error()))
		}
	}

	return nil
}

// Recall retrieves relevant context from the memory store.
// Returns the persona + relevant L1 memories as a context string.
func (pm *PipelineManager) Recall(ctx context.Context, sessionKey, query string) (string, error) {
	_ = sessionKey

	var parts []string

	// Include persona if available.
	if persona := pm.l3.GetPersona(); persona != "" {
		parts = append(parts, "## User Profile\n"+persona)
	}

	// Search L1 memories by query.
	if query != "" {
		// Try vector search if we can embed the query.
		embeddings, err := pm.embedSvc.Embed(ctx, []string{query})
		if err == nil && len(embeddings) > 0 && len(embeddings[0]) > 0 {
			results, err := pm.store.SearchL1Vector(embeddings[0], 10)
			if err == nil && len(results) > 0 {
				parts = append(parts, pm.formatSearchResults(results))
			}
		}

		// FTS fallback.
		if len(parts) <= 1 {
			results, err := pm.store.SearchL1FTS(query, 10)
			if err == nil && len(results) > 0 {
				parts = append(parts, pm.formatSearchResults(results))
			}
		}
	}

	if len(parts) == 0 {
		return "", nil
	}

	return joinParts(parts), nil
}

// Close performs graceful shutdown of the pipeline.
func (pm *PipelineManager) Close() error {
	pm.mu.Lock()
	pm.shutdown = true
	pm.mu.Unlock()

	return pm.store.Close()
}

// runL1 executes L1 extraction and dedup.
func (pm *PipelineManager) runL1(ctx context.Context, records []L0MessageRecord) error {
	pm.l0SinceL1.Store(0)

	// Extract memories from L0 records.
	segments, err := pm.l1.Extract(ctx, records)
	if err != nil {
		return fmt.Errorf("L1 extract: %w", err)
	}

	if len(segments) == 0 {
		return nil
	}

	// Flatten all memories from segments.
	var allMemories []MemoryRecord
	for _, seg := range segments {
		allMemories = append(allMemories, seg.Memories...)
	}

	// Generate embeddings for new memories.
	texts := make([]string, len(allMemories))
	for i, m := range allMemories {
		texts[i] = m.Content
	}

	embeddings, err := pm.embedSvc.Embed(ctx, texts)
	if err != nil {
		pm.log.Warn("embedding failed, proceeding without vectors",
			slog.String("err", err.Error()))
		embeddings = make([][]float32, len(allMemories))
	}

	// Deduplicate.
	decisions, err := pm.l1Dedup.Deduplicate(ctx, allMemories, embeddings)
	if err != nil {
		return fmt.Errorf("L1 dedup: %w", err)
	}

	// Apply decisions.
	if err := pm.l1Dedup.ApplyDecisions(ctx, allMemories, decisions, embeddings); err != nil {
		return fmt.Errorf("L1 apply: %w", err)
	}

	// Count stored memories for L2 triggering.
	stored := int64(0)
	for _, d := range decisions {
		if d.Action != DedupSkip {
			stored++
		}
	}
	pm.l1SinceL2.Add(stored)

	// Check if L2 should trigger.
	if pm.l1SinceL2.Load() >= int64(pm.cfg.L2TriggerAfterRecords) {
		if err := pm.runL2(ctx); err != nil {
			pm.log.Warn("L2 scene extraction failed", slog.String("err", err.Error()))
		}
	}

	return nil
}

// runL2 executes L2 scene extraction.
func (pm *PipelineManager) runL2(ctx context.Context) error {
	pm.l1SinceL2.Store(0)

	results, err := pm.l2.ExtractScenes(ctx)
	if err != nil {
		return fmt.Errorf("L2 extract: %w", err)
	}

	// Check if any result requests persona update.
	personaRequested := false
	for _, r := range results {
		if r.PersonaUpdate {
			personaRequested = true
			break
		}
	}

	pm.l2SinceL3.Add(int64(len(results)))

	// Check if L3 should trigger.
	if personaRequested || pm.l2SinceL3.Load() >= int64(pm.cfg.L3TriggerAfterSceneChanges) {
		if err := pm.runL3(ctx); err != nil {
			pm.log.Warn("L3 persona generation failed", slog.String("err", err.Error()))
		}
	}

	return nil
}

// runL3 executes L3 persona generation.
func (pm *PipelineManager) runL3(ctx context.Context) error {
	pm.l2SinceL3.Store(0)

	_, err := pm.l3.Generate(ctx)
	if err != nil {
		return fmt.Errorf("L3 generate: %w", err)
	}
	return nil
}

// formatSearchResults formats L1 search results as a context string.
func (pm *PipelineManager) formatSearchResults(results []L1SearchResult) string {
	var b []byte
	b = append(b, "## Relevant Memories\n"...)
	for _, r := range results {
		b = append(b, fmt.Sprintf("- [%s] %s\n", r.Record.Type, r.Record.Content)...)
	}
	return string(b)
}

// joinParts combines context parts with double newlines.
func joinParts(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "\n\n"
		}
		result += p
	}
	return result
}
