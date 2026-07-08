package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestL2SceneExtractor_ExtractScenes(t *testing.T) {
	orig := callLLMScene
	callLLMScene = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		results := []L2SceneResult{{
			Strategy:  SceneCreate,
			SceneName: "go_preferences",
			Summary:   "User preferences for Go development",
			Content:   "User prefers Go. Uses VSCode with dark mode.",
		}}
		b, _ := json.Marshal(results)
		return string(b), nil
	}
	t.Cleanup(func() { callLLMScene = orig })

	store := NewInMemoryStore()
	_ = store.UpsertL1(MemoryRecord{
		ID: "m1", Content: "user prefers Go", Type: MemoryTypePersona,
		SceneName: "preferences", CreatedAt: time.Now(),
	}, nil)

	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	ext := NewL2SceneExtractor(cfg, store, nil)
	results, err := ext.ExtractScenes(context.Background())
	if err != nil {
		t.Fatalf("ExtractScenes: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].SceneName != "go_preferences" {
		t.Errorf("SceneName = %q, want go_preferences", results[0].SceneName)
	}
	if results[0].Strategy != SceneCreate {
		t.Errorf("Strategy = %q, want CREATE", results[0].Strategy)
	}
}

func TestL2SceneExtractor_EmptyMemories(t *testing.T) {
	store := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	ext := NewL2SceneExtractor(cfg, store, nil)
	results, err := ext.ExtractScenes(context.Background())
	if err != nil {
		t.Fatalf("ExtractScenes: %v", err)
	}
	if results != nil {
		t.Errorf("want nil results for empty memories, got %v", results)
	}
}

func TestL2SceneExtractor_PersonaUpdateSignal(t *testing.T) {
	orig := callLLMScene
	callLLMScene = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		results := []L2SceneResult{{
			Strategy:  SceneUpdate,
			SceneName: "identity",
			Summary:   "Core user identity",
			Content:   "User is a Go developer. [PERSONA_UPDATE_REQUEST]",
		}}
		b, _ := json.Marshal(results)
		return string(b), nil
	}
	t.Cleanup(func() { callLLMScene = orig })

	store := NewInMemoryStore()
	_ = store.UpsertL1(MemoryRecord{
		ID: "m1", Content: "I am a Go developer", Type: MemoryTypePersona, CreatedAt: time.Now(),
	}, nil)

	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	ext := NewL2SceneExtractor(cfg, store, nil)
	results, err := ext.ExtractScenes(context.Background())
	if err != nil {
		t.Fatalf("ExtractScenes: %v", err)
	}
	if len(results) != 1 || !results[0].PersonaUpdate {
		t.Error("expected PersonaUpdate=true when [PERSONA_UPDATE_REQUEST] is in content")
	}
}

func TestL2SceneExtractor_GetSceneCount(t *testing.T) {
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()
	store := NewInMemoryStore()

	ext := NewL2SceneExtractor(cfg, store, nil)
	count, err := ext.GetSceneCount()
	if err != nil {
		t.Fatalf("GetSceneCount: %v", err)
	}
	if count != 0 {
		t.Errorf("want 0 scenes initially, got %d", count)
	}
}

func TestL2SceneExtractor_FunctionSeam(t *testing.T) {
	var called bool
	orig := callLLMScene
	callLLMScene = func(_ context.Context, _ LLMConfig, sys, user string) (string, error) {
		called = true
		if sys == "" {
			t.Error("system prompt should not be empty")
		}
		return "[]", nil
	}
	t.Cleanup(func() { callLLMScene = orig })

	store := NewInMemoryStore()
	_ = store.UpsertL1(MemoryRecord{
		ID: "m1", Content: "something meaningful for extraction", Type: MemoryTypePersona, CreatedAt: time.Now(),
	}, nil)

	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	ext := NewL2SceneExtractor(cfg, store, nil)
	_, _ = ext.ExtractScenes(context.Background())

	if !called {
		t.Error("callLLMScene was not invoked")
	}
}
