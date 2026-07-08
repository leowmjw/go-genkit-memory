package memory

import (
	"os"
	"path/filepath"
	"time"
)

// MemoryType classifies an extracted L1 memory record.
type MemoryType string

const (
	// MemoryTypePersona captures stable user traits and preferences.
	MemoryTypePersona MemoryType = "persona"
	// MemoryTypeEpisodic captures event-specific contextual memories.
	MemoryTypeEpisodic MemoryType = "episodic"
	// MemoryTypeInstruction captures explicit user directives/rules.
	MemoryTypeInstruction MemoryType = "instruction"
)

// EmbeddingProvider selects between embedding backends.
type EmbeddingProvider string

const (
	// EmbeddingProviderOpenAI uses an OpenAI-compatible HTTP embedding endpoint.
	EmbeddingProviderOpenAI EmbeddingProvider = "openai"
	// EmbeddingProviderONNX uses a local in-process ONNX model for embeddings.
	EmbeddingProviderONNX EmbeddingProvider = "onnx"
)

// ConversationMessage is a single turn in a conversation used as L0 input.
type ConversationMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"` // "user" | "assistant" | "system"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
}

// L0MessageRecord is a persisted L0 entry written to append-only JSONL files.
type L0MessageRecord struct {
	ID         string    `json:"id"`
	SessionKey string    `json:"session_key"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	Timestamp  time.Time `json:"timestamp"`
	CapturedAt time.Time `json:"captured_at"`
}

// MemoryRecord is an L1 atomic memory extracted from conversation turns.
type MemoryRecord struct {
	ID               string     `json:"id"`
	Content          string     `json:"content"`
	Type             MemoryType `json:"type"`
	Priority         int        `json:"priority"` // 0-100
	SceneName        string     `json:"scene_name"`
	SourceMessageIDs []string   `json:"source_message_ids"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	SessionKey       string     `json:"session_key"`
}

// SceneSegment groups memory records extracted by the L1 LLM in a single pass.
type SceneSegment struct {
	SceneName string         `json:"scene_name"`
	Memories  []MemoryRecord `json:"memories"`
}

// DedupDecision represents the outcome of conflict resolution between memories.
type DedupDecision struct {
	Action     DedupAction `json:"action"`
	ExistingID string      `json:"existing_id,omitempty"`
	NewContent string      `json:"new_content,omitempty"`
	Reason     string      `json:"reason,omitempty"`
}

// DedupAction is the action to take for a dedup conflict.
type DedupAction string

const (
	// DedupStore means store the new record (no conflict).
	DedupStore DedupAction = "store"
	// DedupUpdate means update the existing record with new content.
	DedupUpdate DedupAction = "update"
	// DedupMerge means merge the new record into the existing one.
	DedupMerge DedupAction = "merge"
	// DedupSkip means skip the new record (duplicate).
	DedupSkip DedupAction = "skip"
)

// L1SearchResult is a result from vector or FTS search.
type L1SearchResult struct {
	Record    MemoryRecord `json:"record"`
	Score     float64      `json:"score"`
	MatchType string       `json:"match_type"` // "vector" | "fts"
}

// SceneBlock is an L2 scene containing grouped memories with metadata.
type SceneBlock struct {
	Name      string    `json:"name"`
	Summary   string    `json:"summary"`
	Content   string    `json:"content"`
	Heat      int       `json:"heat"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Deleted   bool      `json:"deleted"`
}

// SceneStrategy is the action for L2 scene processing.
type SceneStrategy string

const (
	// SceneCreate creates a new scene block.
	SceneCreate SceneStrategy = "CREATE"
	// SceneUpdate updates an existing scene block.
	SceneUpdate SceneStrategy = "UPDATE"
	// SceneMerge merges two or more scenes together.
	SceneMerge SceneStrategy = "MERGE"
)

// PersonaProfile is the L3 synthesized user persona.
type PersonaProfile struct {
	Content       string    `json:"content"`
	CharCount     int       `json:"char_count"`
	GeneratedAt   time.Time `json:"generated_at"`
	SceneCount    int       `json:"scene_count"`
	IsIncremental bool      `json:"is_incremental"`
}

// PipelineConfig holds all configuration for the local pipeline.
type PipelineConfig struct {
	// DataDir is the base directory for pipeline state (JSONL, scenes, persona).
	// Defaults to ".memory" in the current working directory.
	DataDir string

	// LLM configuration for pipeline processing.
	LLM LLMConfig

	// Embedding configuration.
	Embedding EmbeddingConfig

	// L1TriggerAfterTurns triggers L1 extraction after this many new L0 turns.
	L1TriggerAfterTurns int

	// L2TriggerAfterRecords triggers L2 scene extraction after this many new L1 records.
	L2TriggerAfterRecords int

	// L3TriggerAfterSceneChanges triggers L3 persona generation after this many scene changes.
	L3TriggerAfterSceneChanges int

	// MaxSceneCount is the maximum number of active scenes before merging is forced.
	MaxSceneCount int

	// MaxPersonaChars is the maximum character length of the L3 persona output.
	MaxPersonaChars int
}

// LLMConfig holds configuration for calling an OpenAI-compatible LLM endpoint.
type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// EmbeddingConfig holds configuration for the embedding service.
type EmbeddingConfig struct {
	// Provider selects between "openai" (HTTP API) and "onnx" (local in-process).
	Provider   EmbeddingProvider
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	Timeout    time.Duration
	// ONNXModelPath is the filesystem path to the ONNX model file (used when Provider is "onnx").
	ONNXModelPath string
}

// DefaultPipelineConfig returns a PipelineConfig with sensible defaults.
// DataDir defaults to ".memory" in the current working directory.
func DefaultPipelineConfig() PipelineConfig {
	dataDir := ".memory"
	if wd, err := os.Getwd(); err == nil {
		dataDir = filepath.Join(wd, ".memory")
	}
	return PipelineConfig{
		DataDir: dataDir,
		LLM: LLMConfig{
			Timeout: 30 * time.Second,
		},
		Embedding: EmbeddingConfig{
			Provider:   EmbeddingProviderOpenAI,
			Dimensions: 1536,
			Timeout:    10 * time.Second,
		},
		L1TriggerAfterTurns:        1,
		L2TriggerAfterRecords:      5,
		L3TriggerAfterSceneChanges: 3,
		MaxSceneCount:              15,
		MaxPersonaChars:            2000,
	}
}

// PipelineConfigFromEnv creates a PipelineConfig populated from environment variables.
// Falls back to DefaultPipelineConfig for any unset values.
func PipelineConfigFromEnv() PipelineConfig {
	cfg := DefaultPipelineConfig()

	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
		cfg.Embedding.BaseURL = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
		cfg.Embedding.APIKey = v
	}
	if v := os.Getenv("OPENAI_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("MEMORY_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}

	return cfg
}
