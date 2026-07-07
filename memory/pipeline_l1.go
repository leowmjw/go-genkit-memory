package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// callLLMExtract is a function variable seam for testing.
// It calls the LLM for L1 memory extraction from conversation messages.
var callLLMExtract = func(ctx context.Context, cfg LLMConfig, systemPrompt, userPrompt string) (string, error) {
	// Stub: real implementation calls OpenAI-compatible chat completions endpoint.
	_ = ctx
	_ = cfg
	_ = systemPrompt
	_ = userPrompt
	return "[]", nil
}

// L1Extractor extracts atomic memories from L0 conversation records using LLM.
type L1Extractor struct {
	cfg PipelineConfig
	log *slog.Logger
}

// NewL1Extractor creates a new L1 extractor with the given configuration.
func NewL1Extractor(cfg PipelineConfig, log *slog.Logger) *L1Extractor {
	if log == nil {
		log = slog.Default()
	}
	return &L1Extractor{cfg: cfg, log: log}
}

// Extract processes L0 records through the quality gate and LLM extraction.
// Returns scene segments containing extracted memory records.
func (e *L1Extractor) Extract(ctx context.Context, records []L0MessageRecord) ([]SceneSegment, error) {
	// Filter through quality gate.
	var eligible []L0MessageRecord
	for _, r := range records {
		if ShouldExtractL1(r.Content) {
			eligible = append(eligible, r)
		} else {
			e.log.Debug("L1 quality gate rejected message",
				slog.String("id", r.ID),
				slog.String("role", r.Role),
			)
		}
	}

	if len(eligible) == 0 {
		return nil, nil
	}

	// Build the user prompt from eligible messages.
	userPrompt := e.buildUserPrompt(eligible)

	// Call LLM for extraction.
	raw, err := callLLMExtract(ctx, e.cfg.LLM, l1ExtractionSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("pipeline_l1: llm extract: %w", err)
	}

	// Parse the response.
	segments, err := e.parseExtraction(raw, eligible)
	if err != nil {
		return nil, fmt.Errorf("pipeline_l1: parse extraction: %w", err)
	}

	return segments, nil
}

// buildUserPrompt formats L0 records into a prompt for the LLM.
func (e *L1Extractor) buildUserPrompt(records []L0MessageRecord) string {
	var b strings.Builder
	b.WriteString("Extract memories from the following conversation messages:\n\n")
	for _, r := range records {
		b.WriteString(fmt.Sprintf("[%s] %s: %s\n", r.Timestamp.Format(time.RFC3339), r.Role, r.Content))
	}
	return b.String()
}

// parseExtraction parses the LLM JSON response into SceneSegments.
// Handles markdown code fences that LLMs sometimes wrap JSON in.
func (e *L1Extractor) parseExtraction(raw string, sources []L0MessageRecord) ([]SceneSegment, error) {
	// Strip markdown code fences if present.
	cleaned := stripJSONFences(raw)

	var segments []SceneSegment
	if err := json.Unmarshal([]byte(cleaned), &segments); err != nil {
		return nil, fmt.Errorf("parse JSON: %w (raw: %s)", err, truncate(raw, 200))
	}

	// Enrich records with metadata.
	now := time.Now()
	sourceIDs := make([]string, len(sources))
	for i, s := range sources {
		sourceIDs[i] = s.ID
	}
	for i := range segments {
		for j := range segments[i].Memories {
			m := &segments[i].Memories[j]
			if m.ID == "" {
				m.ID = generateID()
			}
			if m.CreatedAt.IsZero() {
				m.CreatedAt = now
			}
			m.UpdatedAt = now
			if len(m.SourceMessageIDs) == 0 {
				m.SourceMessageIDs = sourceIDs
			}
		}
	}

	return segments, nil
}

// stripJSONFences removes markdown code fences from LLM output.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSuffix(s, "```")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// truncate shortens a string to maxLen characters for logging.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
