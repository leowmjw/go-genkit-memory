package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// callLLMScene is a function variable seam for testing.
// It calls the LLM for L2 scene extraction/update/merge.
var callLLMScene = func(ctx context.Context, cfg LLMConfig, systemPrompt, userPrompt string) (string, error) {
	return callChatCompletion(ctx, cfg, systemPrompt, userPrompt)
}

// L2SceneExtractor groups L1 memories into scene blocks (.md files).
type L2SceneExtractor struct {
	cfg      PipelineConfig
	store    MemoryStore
	log      *slog.Logger
	sceneDir string
}

// NewL2SceneExtractor creates a new scene extractor.
func NewL2SceneExtractor(cfg PipelineConfig, store MemoryStore, log *slog.Logger) *L2SceneExtractor {
	if log == nil {
		log = slog.Default()
	}
	return &L2SceneExtractor{
		cfg:      cfg,
		store:    store,
		log:      log,
		sceneDir: filepath.Join(cfg.DataDir, "scenes"),
	}
}

// L2SceneResult holds the output of a scene extraction run.
type L2SceneResult struct {
	Strategy      SceneStrategy `json:"strategy"`
	SceneName     string        `json:"scene_name"`
	Content       string        `json:"content"`
	Summary       string        `json:"summary"`
	PersonaUpdate bool          `json:"persona_update"` // true if [PERSONA_UPDATE_REQUEST] detected
}

// ExtractScenes processes accumulated L1 memories into scene blocks.
// Returns scene results and whether a persona update was requested.
func (e *L2SceneExtractor) ExtractScenes(ctx context.Context) ([]L2SceneResult, error) {
	// Get all L1 memories for scene grouping.
	memories, err := e.store.GetAllL1()
	if err != nil {
		return nil, fmt.Errorf("pipeline_l2: get L1 memories: %w", err)
	}

	if len(memories) == 0 {
		return nil, nil
	}

	// Load existing scenes.
	existingScenes, err := e.loadScenes()
	if err != nil {
		return nil, fmt.Errorf("pipeline_l2: load scenes: %w", err)
	}

	// Build prompt with memories and existing scenes.
	userPrompt := e.buildScenePrompt(memories, existingScenes)

	// Call LLM for scene extraction.
	raw, err := callLLMScene(ctx, e.cfg.LLM, l2SceneSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("pipeline_l2: llm scene: %w", err)
	}

	// Parse results.
	results, err := e.parseSceneResults(raw)
	if err != nil {
		return nil, fmt.Errorf("pipeline_l2: parse results: %w", err)
	}

	// Apply results — write scene files.
	for _, r := range results {
		if err := e.applySceneResult(r, existingScenes); err != nil {
			e.log.Warn("failed to apply scene result",
				slog.String("scene", r.SceneName),
				slog.String("err", err.Error()),
			)
		}
	}

	return results, nil
}

// GetSceneCount returns the number of active (non-deleted) scenes.
func (e *L2SceneExtractor) GetSceneCount() (int, error) {
	scenes, err := e.loadScenes()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, s := range scenes {
		if !s.Deleted {
			count++
		}
	}
	return count, nil
}

// loadScenes reads all scene block files from the scene directory.
func (e *L2SceneExtractor) loadScenes() ([]SceneBlock, error) {
	if err := os.MkdirAll(e.sceneDir, 0750); err != nil {
		return nil, fmt.Errorf("mkdir scenes: %w", err)
	}

	entries, err := os.ReadDir(e.sceneDir)
	if err != nil {
		return nil, fmt.Errorf("read scenes dir: %w", err)
	}

	var scenes []SceneBlock
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.sceneDir, entry.Name()))
		if err != nil {
			continue
		}
		var scene SceneBlock
		if err := json.Unmarshal(data, &scene); err != nil {
			continue
		}
		scenes = append(scenes, scene)
	}
	return scenes, nil
}

// buildScenePrompt formats memories and existing scenes for the LLM.
func (e *L2SceneExtractor) buildScenePrompt(memories []MemoryRecord, scenes []SceneBlock) string {
	var b strings.Builder
	b.WriteString("Current scene count: ")
	b.WriteString(fmt.Sprintf("%d / %d max\n\n", len(scenes), e.cfg.MaxSceneCount))

	b.WriteString("Existing scenes:\n")
	for _, s := range scenes {
		if s.Deleted {
			continue
		}
		b.WriteString(fmt.Sprintf("  - %s (heat: %d): %s\n", s.Name, s.Heat, s.Summary))
	}

	b.WriteString("\nNew memories to process:\n")
	for _, m := range memories {
		b.WriteString(fmt.Sprintf("  - [%s] %s: %s\n", m.Type, m.SceneName, m.Content))
	}

	return b.String()
}

// parseSceneResults parses the LLM response into scene results.
func (e *L2SceneExtractor) parseSceneResults(raw string) ([]L2SceneResult, error) {
	cleaned := stripJSONFences(raw)

	var results []L2SceneResult
	if err := json.Unmarshal([]byte(cleaned), &results); err != nil {
		// Try single object.
		var single L2SceneResult
		if err2 := json.Unmarshal([]byte(cleaned), &single); err2 == nil {
			results = []L2SceneResult{single}
		} else {
			return nil, fmt.Errorf("parse scene results: %w", err)
		}
	}

	// Check for persona update signal.
	for i, r := range results {
		if strings.Contains(r.Content, "[PERSONA_UPDATE_REQUEST]") {
			results[i].PersonaUpdate = true
		}
	}

	return results, nil
}

// applySceneResult writes or updates a scene file.
func (e *L2SceneExtractor) applySceneResult(result L2SceneResult, existing []SceneBlock) error {
	if err := os.MkdirAll(e.sceneDir, 0750); err != nil {
		return err
	}

	now := time.Now()
	var scene SceneBlock

	switch result.Strategy {
	case SceneCreate:
		scene = SceneBlock{
			Name:      result.SceneName,
			Summary:   result.Summary,
			Content:   result.Content,
			Heat:      1,
			CreatedAt: now,
			UpdatedAt: now,
		}
	case SceneUpdate:
		// Find existing and update.
		for _, s := range existing {
			if s.Name == result.SceneName {
				scene = s
				break
			}
		}
		scene.Content = result.Content
		scene.Summary = result.Summary
		scene.Heat++
		scene.UpdatedAt = now
	case SceneMerge:
		scene = SceneBlock{
			Name:      result.SceneName,
			Summary:   result.Summary,
			Content:   result.Content,
			Heat:      1,
			CreatedAt: now,
			UpdatedAt: now,
		}
	default:
		return fmt.Errorf("unknown strategy: %s", result.Strategy)
	}

	data, err := json.MarshalIndent(scene, "", "  ")
	if err != nil {
		return err
	}

	filename := sanitizeFilename(scene.Name) + ".json"
	return os.WriteFile(filepath.Join(e.sceneDir, filename), data, 0640)
}

// sanitizeFilename makes a string safe for use as a filename.
func sanitizeFilename(name string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_", ".", "_")
	return strings.ToLower(r.Replace(name))
}
