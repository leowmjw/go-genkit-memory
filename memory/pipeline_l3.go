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

// callLLMPersona is a function variable seam for testing.
// It calls the LLM for L3 persona generation (outputs markdown, not JSON).
var callLLMPersona = func(ctx context.Context, cfg LLMConfig, systemPrompt, userPrompt string) (string, error) {
	return callChatCompletionText(ctx, cfg, systemPrompt, userPrompt)
}

// L3PersonaGenerator synthesizes a user persona from scene blocks.
type L3PersonaGenerator struct {
	cfg         PipelineConfig
	log         *slog.Logger
	personaPath string
	sceneDir    string
}

// NewL3PersonaGenerator creates a new persona generator.
func NewL3PersonaGenerator(cfg PipelineConfig, log *slog.Logger) *L3PersonaGenerator {
	if log == nil {
		log = slog.Default()
	}
	return &L3PersonaGenerator{
		cfg:         cfg,
		log:         log,
		personaPath: filepath.Join(cfg.DataDir, "persona.md"),
		sceneDir:    filepath.Join(cfg.DataDir, "scenes"),
	}
}

// Generate creates or updates the persona profile from scene blocks.
// Returns the generated persona profile.
func (g *L3PersonaGenerator) Generate(ctx context.Context) (*PersonaProfile, error) {
	// Load existing persona (if any) for incremental mode.
	existingPersona := g.loadExistingPersona()
	isIncremental := existingPersona != ""

	// Load all scene blocks.
	scenes, err := g.loadScenes()
	if err != nil {
		return nil, fmt.Errorf("pipeline_l3: load scenes: %w", err)
	}

	if len(scenes) == 0 {
		return nil, nil
	}

	// Build prompt.
	userPrompt := g.buildPersonaPrompt(scenes, existingPersona)

	// Call LLM.
	raw, err := callLLMPersona(ctx, g.cfg.LLM, l3PersonaSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("pipeline_l3: llm persona: %w", err)
	}

	// Enforce character limit.
	content := strings.TrimSpace(raw)
	if g.cfg.MaxPersonaChars > 0 && len(content) > g.cfg.MaxPersonaChars {
		content = content[:g.cfg.MaxPersonaChars]
	}

	// Write persona file.
	if err := g.writePersona(content); err != nil {
		return nil, fmt.Errorf("pipeline_l3: write persona: %w", err)
	}

	profile := &PersonaProfile{
		Content:       content,
		CharCount:     len(content),
		GeneratedAt:   time.Now(),
		SceneCount:    len(scenes),
		IsIncremental: isIncremental,
	}

	g.log.Debug("persona generated",
		slog.Int("char_count", profile.CharCount),
		slog.Int("scene_count", profile.SceneCount),
		slog.Bool("incremental", profile.IsIncremental),
	)

	return profile, nil
}

// GetPersona returns the current persona content, or empty string if none exists.
func (g *L3PersonaGenerator) GetPersona() string {
	return g.loadExistingPersona()
}

// loadExistingPersona reads the current persona file.
func (g *L3PersonaGenerator) loadExistingPersona() string {
	data, err := os.ReadFile(g.personaPath)
	if err != nil {
		return ""
	}
	return string(data)
}

// loadScenes reads all active scene blocks from the scene directory.
func (g *L3PersonaGenerator) loadScenes() ([]SceneBlock, error) {
	if err := os.MkdirAll(g.sceneDir, 0750); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(g.sceneDir)
	if err != nil {
		return nil, err
	}

	var scenes []SceneBlock
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(g.sceneDir, entry.Name()))
		if err != nil {
			continue
		}
		var scene SceneBlock
		if json.Unmarshal(data, &scene) == nil && !scene.Deleted {
			scenes = append(scenes, scene)
		}
	}
	return scenes, nil
}

// buildPersonaPrompt formats scenes and existing persona for the LLM.
func (g *L3PersonaGenerator) buildPersonaPrompt(scenes []SceneBlock, existingPersona string) string {
	var b strings.Builder

	if existingPersona != "" {
		b.WriteString("Existing persona (update incrementally):\n")
		b.WriteString(existingPersona)
		b.WriteString("\n\n")
	} else {
		b.WriteString("No existing persona. Generate from scratch.\n\n")
	}

	b.WriteString(fmt.Sprintf("Scene blocks (%d total):\n", len(scenes)))
	for _, s := range scenes {
		b.WriteString(fmt.Sprintf("\n## %s\nSummary: %s\nHeat: %d\n%s\n",
			s.Name, s.Summary, s.Heat, s.Content))
	}

	b.WriteString(fmt.Sprintf("\nConstraint: output must be ≤ %d characters.\n", g.cfg.MaxPersonaChars))
	return b.String()
}

// writePersona writes the persona content to disk.
func (g *L3PersonaGenerator) writePersona(content string) error {
	if err := os.MkdirAll(filepath.Dir(g.personaPath), 0750); err != nil {
		return err
	}
	return os.WriteFile(g.personaPath, []byte(content), 0640)
}
