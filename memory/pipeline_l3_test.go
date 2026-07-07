package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestL3PersonaGenerator_Generate(t *testing.T) {
	orig := callLLMPersona
	callLLMPersona = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		return "# User Persona\n\nGo developer who prefers concise code.", nil
	}
	t.Cleanup(func() { callLLMPersona = orig })

	dir := t.TempDir()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = dir

	// Create a scene file.
	sceneDir := filepath.Join(dir, "scenes")
	_ = os.MkdirAll(sceneDir, 0750)
	scene := SceneBlock{Name: "prefs", Summary: "User preferences", Content: "prefers Go", Heat: 3}
	data, _ := json.Marshal(scene)
	_ = os.WriteFile(filepath.Join(sceneDir, "prefs.json"), data, 0640)

	gen := NewL3PersonaGenerator(cfg, nil)
	profile, err := gen.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if profile == nil {
		t.Fatal("expected profile, got nil")
	}
	if !strings.Contains(profile.Content, "Go developer") {
		t.Errorf("Content = %q, want to contain 'Go developer'", profile.Content)
	}
	if profile.SceneCount != 1 {
		t.Errorf("SceneCount = %d, want 1", profile.SceneCount)
	}
	if profile.IsIncremental {
		t.Error("expected IsIncremental=false for first generation")
	}
}

func TestL3PersonaGenerator_IncrementalMode(t *testing.T) {
	orig := callLLMPersona
	callLLMPersona = func(_ context.Context, _ LLMConfig, _, userPrompt string) (string, error) {
		if !strings.Contains(userPrompt, "Existing persona") {
			t.Error("expected incremental prompt to include existing persona")
		}
		return "Updated persona with new facts.", nil
	}
	t.Cleanup(func() { callLLMPersona = orig })

	dir := t.TempDir()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = dir

	// Write existing persona.
	_ = os.WriteFile(filepath.Join(dir, "persona.md"), []byte("Old persona content"), 0640)

	// Create a scene.
	sceneDir := filepath.Join(dir, "scenes")
	_ = os.MkdirAll(sceneDir, 0750)
	scene := SceneBlock{Name: "new_info", Summary: "New info", Content: "learned something"}
	data, _ := json.Marshal(scene)
	_ = os.WriteFile(filepath.Join(sceneDir, "new_info.json"), data, 0640)

	gen := NewL3PersonaGenerator(cfg, nil)
	profile, err := gen.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !profile.IsIncremental {
		t.Error("expected IsIncremental=true when existing persona exists")
	}
}

func TestL3PersonaGenerator_CharLimit(t *testing.T) {
	orig := callLLMPersona
	callLLMPersona = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		return strings.Repeat("x", 5000), nil
	}
	t.Cleanup(func() { callLLMPersona = orig })

	dir := t.TempDir()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = dir
	cfg.MaxPersonaChars = 2000

	sceneDir := filepath.Join(dir, "scenes")
	_ = os.MkdirAll(sceneDir, 0750)
	scene := SceneBlock{Name: "test", Summary: "test", Content: "test"}
	data, _ := json.Marshal(scene)
	_ = os.WriteFile(filepath.Join(sceneDir, "test.json"), data, 0640)

	gen := NewL3PersonaGenerator(cfg, nil)
	profile, err := gen.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if profile.CharCount > 2000 {
		t.Errorf("CharCount = %d, want ≤ 2000", profile.CharCount)
	}
}

func TestL3PersonaGenerator_NoScenes(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = dir

	gen := NewL3PersonaGenerator(cfg, nil)
	profile, err := gen.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if profile != nil {
		t.Errorf("expected nil profile when no scenes exist, got %v", profile)
	}
}

func TestL3PersonaGenerator_GetPersona(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = dir

	gen := NewL3PersonaGenerator(cfg, nil)

	// No persona file yet.
	if p := gen.GetPersona(); p != "" {
		t.Errorf("expected empty persona, got %q", p)
	}

	// Write one.
	_ = os.WriteFile(filepath.Join(dir, "persona.md"), []byte("my persona"), 0640)
	if p := gen.GetPersona(); p != "my persona" {
		t.Errorf("GetPersona = %q, want 'my persona'", p)
	}
}
