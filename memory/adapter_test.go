package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/firebase/genkit/go/core/x/session"
)

// ─── minimal in-memory store for tests ───────────────────────────────────────

type memState struct {
	Value string `json:"value"`
}

type testStore struct {
	mu   sync.Mutex
	data map[string]*session.Data[memState]
}

func newTestStore() *testStore {
	return &testStore{data: make(map[string]*session.Data[memState])}
}

func (s *testStore) Get(_ context.Context, id string) (*session.Data[memState], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[id], nil
}

func (s *testStore) Save(_ context.Context, id string, d *session.Data[memState]) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = d
	return nil
}

func (s *testStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}

func (s *testStore) Close() error { return nil }

// ─── test helpers ─────────────────────────────────────────────────────────────

// stubAllLLM replaces all LLM function seams with stubs for testing.
// Returns a cleanup function that restores originals.
func stubAllLLM(t *testing.T) {
	t.Helper()

	origExtract := callLLMExtract
	callLLMExtract = func(_ context.Context, _ LLMConfig, _, _ string) (string, error) {
		segments := []SceneSegment{{
			SceneName: "test",
			Memories: []MemoryRecord{{
				Content:  "test memory",
				Type:     MemoryTypePersona,
				Priority: 50,
			}},
		}}
		b, _ := json.Marshal(segments)
		return string(b), nil
	}
	t.Cleanup(func() { callLLMExtract = origExtract })

	origEmbed := embedBatch
	embedBatch = func(_ context.Context, _ EmbeddingConfig, texts []string) ([][]float32, error) {
		result := make([][]float32, len(texts))
		for i := range texts {
			result[i] = []float32{0.1, 0.2, 0.3}
		}
		return result, nil
	}
	t.Cleanup(func() { embedBatch = origEmbed })

	origWrite := writeJSONL
	writeJSONL = func(_ string, _ []L0MessageRecord) error { return nil }
	t.Cleanup(func() { writeJSONL = origWrite })
}

func newTestAdapter(t *testing.T) *Adapter[memState] {
	t.Helper()
	store := newTestStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()
	log := slog.New(slog.NewJSONHandler(nil, &slog.HandlerOptions{Level: slog.LevelError}))
	// Use discard logger for tests.
	log = slog.Default()

	return NewAdapter[memState](store,
		WithPipelineConfig(cfg),
		WithLogger(log),
	)
}

// ─── adapter unit tests ───────────────────────────────────────────────────────

// TestAdapter_GetSaveDelegate verifies that Get/Save pass through to the underlying store.
func TestAdapter_GetSaveDelegate(t *testing.T) {
	stubAllLLM(t)

	store := newTestStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()
	a := NewAdapter[memState](store, WithPipelineConfig(cfg))
	defer a.Close()

	ctx := context.Background()
	d := &session.Data[memState]{ID: "s1", State: memState{Value: "hello"}}

	if err := a.Save(ctx, "s1", d); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := a.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.State.Value != "hello" {
		t.Errorf("want value=hello, got %v", got)
	}
}

// TestAdapter_DeleteDelegate verifies Delete passes through to the underlying store.
func TestAdapter_DeleteDelegate(t *testing.T) {
	stubAllLLM(t)

	store := newTestStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()
	a := NewAdapter[memState](store, WithPipelineConfig(cfg))
	defer a.Close()

	ctx := context.Background()
	_ = a.Save(ctx, "del-1", &session.Data[memState]{ID: "del-1", State: memState{Value: "x"}})
	_ = a.Delete(ctx, "del-1")

	got, err := a.Get(ctx, "del-1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %v", got)
	}
}

// TestAdapter_CaptureProcessesThroughPipeline verifies that Capture feeds the local pipeline.
func TestAdapter_CaptureProcessesThroughPipeline(t *testing.T) {
	stubAllLLM(t)

	store := newTestStore()
	memStore := NewInMemoryStore()
	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()
	cfg.L1TriggerAfterTurns = 1

	a := NewAdapter[memState](store,
		WithPipelineConfig(cfg),
		WithMemoryStore(memStore),
	)
	defer a.Close()

	ctx := context.Background()
	if err := a.Capture(ctx, "sess-1", "hello there user message", "world assistant response"); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Verify L1 memory was stored via pipeline.
	all, _ := memStore.GetAllL1()
	if len(all) != 1 {
		t.Errorf("want 1 L1 memory stored, got %d", len(all))
	}
}

// TestAdapter_RecallReturnsTrimmedContext verifies token budget trimming.
func TestAdapter_RecallReturnsTrimmedContext(t *testing.T) {
	stubAllLLM(t)

	memStore := NewInMemoryStore()
	// Insert a memory so recall has something to return.
	_ = memStore.UpsertL1(MemoryRecord{
		ID: "m1", Content: strings.Repeat("x", 200), Type: MemoryTypePersona,
		Priority: 80, CreatedAt: time.Now(),
	}, []float32{0.1, 0.2, 0.3})

	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	a := NewAdapter[memState](newTestStore(),
		WithPipelineConfig(cfg),
		WithMemoryStore(memStore),
		WithTokenBudget(100),
	)
	defer a.Close()

	ctx := context.Background()
	text, err := a.Recall(ctx, "sess-1", "query something")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(text) > 100 {
		t.Errorf("expected ≤ 100 chars, got %d", len(text))
	}
}

// TestAdapter_RecallGracefulDegradation verifies that Recall returns empty
// string (no error) when the pipeline has no relevant data.
func TestAdapter_RecallGracefulDegradation(t *testing.T) {
	stubAllLLM(t)

	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	a := NewAdapter[memState](newTestStore(), WithPipelineConfig(cfg))
	defer a.Close()

	ctx := context.Background()
	text, err := a.Recall(ctx, "sess-1", "query something")
	if err != nil {
		t.Fatalf("Recall should not error, got: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty string for empty store, got %q", text)
	}
}

// TestAdapter_CaptureBuffersOnPipelineFailure verifies fallback buffering.
func TestAdapter_CaptureBuffersOnPipelineFailure(t *testing.T) {
	// Make writeJSONL fail to simulate pipeline error.
	origWrite := writeJSONL
	writeJSONL = func(_ string, _ []L0MessageRecord) error {
		return fmt.Errorf("simulated write failure")
	}
	t.Cleanup(func() { writeJSONL = origWrite })

	origEmbed := embedBatch
	embedBatch = func(_ context.Context, _ EmbeddingConfig, texts []string) ([][]float32, error) {
		return make([][]float32, len(texts)), nil
	}
	t.Cleanup(func() { embedBatch = origEmbed })

	cfg := DefaultPipelineConfig()
	cfg.DataDir = t.TempDir()

	a := NewAdapter[memState](newTestStore(), WithPipelineConfig(cfg))
	defer a.Close()

	ctx := context.Background()
	for i := range 3 {
		_ = a.Capture(ctx, "sess-1", fmt.Sprintf("user%d msg", i), fmt.Sprintf("assist%d response", i))
	}

	if n := a.FallbackLen(); n != 3 {
		t.Errorf("want 3 buffered, got %d", n)
	}
}

// ─── fallback cache tests ─────────────────────────────────────────────────────

// TestFallbackCache_RingOverwrite verifies the ring buffer evicts oldest entries.
func TestFallbackCache_RingOverwrite(t *testing.T) {
	fb := newFallbackCache(3)
	for i := range 5 {
		fb.Add(captureEntry{SessionKey: fmt.Sprintf("s%d", i)})
	}
	if fb.Len() != 3 {
		t.Errorf("want 3 entries, got %d", fb.Len())
	}
	entries := fb.DrainAll()
	// The last 3 written (s2, s3, s4) should be present.
	keys := make(map[string]bool)
	for _, e := range entries {
		keys[e.SessionKey] = true
	}
	for _, k := range []string{"s2", "s3", "s4"} {
		if !keys[k] {
			t.Errorf("expected key %q in drained entries", k)
		}
	}
}

// TestFallbackCache_DrainResets verifies DrainAll empties the buffer.
func TestFallbackCache_DrainResets(t *testing.T) {
	fb := newFallbackCache(10)
	fb.Add(captureEntry{SessionKey: "a"})
	fb.Add(captureEntry{SessionKey: "b"})
	_ = fb.DrainAll()
	if fb.Len() != 0 {
		t.Errorf("want 0 after drain, got %d", fb.Len())
	}
}

// ─── sanitize tests ───────────────────────────────────────────────────────────

// TestSanitizeRole verifies invalid roles are replaced with "user".
func TestSanitizeRole(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"user", "user"},
		{"assistant", "assistant"},
		{"system", "system"},
		{"admin", "user"},
		{"root", "user"},
		{"ADMIN", "user"},
		{"", "user"},
		{"  User  ", "user"},
	}
	for _, tc := range cases {
		got := SanitizeRole(tc.in)
		if got != tc.want {
			t.Errorf("SanitizeRole(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSanitizeContent_InvalidUTF8 verifies corrupt bytes are replaced.
func TestSanitizeContent_InvalidUTF8(t *testing.T) {
	bad := "hello \xff\xfe world"
	out, err := SanitizeContent(bad)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "\xff") || strings.Contains(out, "\xfe") {
		t.Errorf("invalid UTF-8 bytes survived sanitization: %q", out)
	}
}

// TestSanitizeContent_LongToken verifies zero-delimiter attacks are truncated.
func TestSanitizeContent_LongToken(t *testing.T) {
	// Build a 25 KB continuous string with no delimiters.
	giant := strings.Repeat("a", 25*1024)
	out, err := SanitizeContent(giant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > maxTokenLen+100 {
		t.Errorf("zero-delimiter attack not truncated: len=%d", len(out))
	}
}

// TestCheckJSONDepth_Exceeds verifies deep nesting is rejected.
func TestCheckJSONDepth_Exceeds(t *testing.T) {
	// Build a 150-level deep nested object.
	var b strings.Builder
	for range 150 {
		b.WriteString(`{"a":`)
	}
	b.WriteString(`"leaf"`)
	for range 150 {
		b.WriteString("}")
	}
	err := CheckJSONDepth([]byte(b.String()), maxJSONDepth)
	if err == nil {
		t.Error("expected error for depth > 100, got nil")
	}
}

// TestCheckJSONDepth_OK verifies shallow nesting passes.
func TestCheckJSONDepth_OK(t *testing.T) {
	data := []byte(`{"a":{"b":{"c":"leaf"}}}`)
	if err := CheckJSONDepth(data, maxJSONDepth); err != nil {
		t.Errorf("unexpected error for shallow JSON: %v", err)
	}
}

// ─── offload tests ────────────────────────────────────────────────────────────

// TestOffloader_SmallContentPassthrough verifies small content is unchanged.
func TestOffloader_SmallContentPassthrough(t *testing.T) {
	off := newOffloader(t.TempDir(), slog.Default())
	content := "short content"
	out, err := off.MaybeOffload("sess", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != content {
		t.Errorf("want unchanged content, got %q", out)
	}
}

// TestOffloader_LargeContentWrittenToFile verifies payloads > 50 KB are offloaded.
func TestOffloader_LargeContentWrittenToFile(t *testing.T) {
	dir := t.TempDir()
	off := newOffloader(dir, slog.Default())
	large := strings.Repeat("x", offloadThreshold+1)
	ptr, err := off.MaybeOffload("sess-42", large)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ptr == large {
		t.Fatal("expected a path pointer, got original content")
	}
	if !strings.HasSuffix(ptr, ".md") {
		t.Errorf("expected a .md path pointer, got %q", ptr)
	}
}
