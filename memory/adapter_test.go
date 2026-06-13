package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

// ─── helpers ──────────────────────────────────────────────────────────────────

// fakeGateway runs a minimal HTTP server that records calls and returns
// configurable responses.
type fakeGateway struct {
	captureCount atomic.Int64
	recallResult string
	healthStatus string
	slowFor      time.Duration // if set, /recall sleeps for this duration
	srv          *httptest.Server
}

func newFakeGateway(recallResult, healthStatus string) *fakeGateway {
	fg := &fakeGateway{recallResult: recallResult, healthStatus: healthStatus}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /capture", func(w http.ResponseWriter, _ *http.Request) {
		fg.captureCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /recall", func(w http.ResponseWriter, r *http.Request) {
		if fg.slowFor > 0 {
			select {
			case <-time.After(fg.slowFor):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RecallResponse{Context: fg.recallResult})
	})
	mux.HandleFunc("POST /session/end", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(HealthResponse{Status: fg.healthStatus})
	})
	fg.srv = httptest.NewServer(mux)
	return fg
}

func (fg *fakeGateway) Close() { fg.srv.Close() }

// ─── adapter unit tests ───────────────────────────────────────────────────────

// TestAdapter_GetSaveDelegate verifies that Get/Save pass through to the underlying store.
func TestAdapter_GetSaveDelegate(t *testing.T) {
	fg := newFakeGateway("", "ok")
	defer fg.Close()

	store := newTestStore()
	a := NewAdapter[memState](store, WithGatewayURL(fg.srv.URL))
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
	fg := newFakeGateway("", "ok")
	defer fg.Close()

	store := newTestStore()
	a := NewAdapter[memState](store, WithGatewayURL(fg.srv.URL))
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

// TestAdapter_CaptureReachesGateway verifies that Capture fires an HTTP call.
func TestAdapter_CaptureReachesGateway(t *testing.T) {
	fg := newFakeGateway("", "ok")
	defer fg.Close()

	a := NewAdapter[memState](newTestStore(), WithGatewayURL(fg.srv.URL))
	defer a.Close()

	ctx := context.Background()
	if err := a.Capture(ctx, "sess-1", "hello", "world"); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Give the goroutine time to fire.
	time.Sleep(50 * time.Millisecond)
	if n := fg.captureCount.Load(); n != 1 {
		t.Errorf("want 1 capture, got %d", n)
	}
}

// TestAdapter_RecallReturnsTrimmedContext verifies token budget trimming.
func TestAdapter_RecallReturnsTrimmedContext(t *testing.T) {
	fg := newFakeGateway(strings.Repeat("x", 200), "ok")
	defer fg.Close()

	a := NewAdapter[memState](newTestStore(), WithGatewayURL(fg.srv.URL), WithTokenBudget(100))
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	text, err := a.Recall(ctx, "sess-1", "query")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(text) > 100 {
		t.Errorf("expected ≤ 100 chars, got %d", len(text))
	}
}

// TestAdapter_RecallGracefulDegradation verifies that Recall returns empty
// string (no error) when the gateway is unreachable.
func TestAdapter_RecallGracefulDegradation(t *testing.T) {
	a := NewAdapter[memState](newTestStore(),
		WithGatewayURL("http://127.0.0.1:19999")) // nothing listening
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	text, err := a.Recall(ctx, "sess-1", "query")
	if err != nil {
		t.Fatalf("Recall should not surface errors on gateway failure, got: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty string on degradation, got %q", text)
	}
}

// TestAdapter_CaptureBuffersOnGatewayFailure verifies fallback buffering.
func TestAdapter_CaptureBuffersOnGatewayFailure(t *testing.T) {
	a := NewAdapter[memState](newTestStore(),
		WithGatewayURL("http://127.0.0.1:19999"))
	defer a.Close()

	ctx := context.Background()
	for i := range 3 {
		_ = a.Capture(ctx, "sess-1", fmt.Sprintf("user%d", i), fmt.Sprintf("assist%d", i))
	}

	// Allow goroutines to fail and buffer.
	time.Sleep(200 * time.Millisecond)
	if n := a.FallbackLen(); n != 3 {
		t.Errorf("want 3 buffered, got %d", n)
	}
}

// TestAdapter_SlowGatewayTimeout verifies the 5 s recall timeout fires.
func TestAdapter_SlowGatewayTimeout(t *testing.T) {
	fg := newFakeGateway("ctx", "ok")
	fg.slowFor = 8 * time.Second // longer than gatewayTimeout (5 s)
	defer fg.Close()

	a := NewAdapter[memState](newTestStore(), WithGatewayURL(fg.srv.URL))
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	text, err := a.Recall(ctx, "sess-1", "query")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Recall should degrade gracefully, not error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty string on timeout, got %q", text)
	}
	if elapsed > 6*time.Second {
		t.Errorf("timeout took too long: %v (want < 6 s)", elapsed)
	}
}

// TestAdapter_CircuitBreaker verifies the circuit breaker opens after 5 fails.
func TestAdapter_CircuitBreaker(t *testing.T) {
	// Use a server that always returns 500.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	c := newGatewayClient(failSrv.URL, "", nil)
	if c.log == nil {
		// replace nil logger with discard
		c.log = slog.Default()
	}
	ctx := context.Background()

	// Trigger failures synchronously by calling post directly.
	for i := range circuitBreakerThreshold {
		_ = c.post(ctx, "/capture", CaptureRequest{}, nil)
		_ = i
	}

	if !c.isOpen() {
		t.Error("circuit breaker should be open after 5 consecutive failures")
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
