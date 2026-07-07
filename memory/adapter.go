package memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/firebase/genkit/go/core/x/session"
)

// defaultTokenBudget is the maximum number of characters returned by Recall.
// 0 means no limit.
const defaultTokenBudget = 0

// Adapter wraps any [session.Store] and layers the 4-tier memory pipeline
// on top for long-term memory. It is safe for concurrent use.
//
// The adapter implements [session.Store] so it is a drop-in replacement
// wherever a store is accepted (session.New, session.Load, etc.).
//
// Additionally it exposes:
//   - [Adapter.Capture]: L0 turn capture feeding the local pipeline (L0→L3).
//   - [Adapter.Recall]: L1–L3 context retrieval from the local memory store.
//   - [Adapter.EndSession]: flush pending pipeline work on session end.
type Adapter[S any] struct {
	store    session.Store[S]
	pipeline *PipelineManager
	fb       *fallbackCache
	off      *offloader
	log      *slog.Logger
	budget   int
}

// adapterOptions collects functional options.
type adapterOptions struct {
	pipelineCfg PipelineConfig
	memStore    MemoryStore
	refsDir     string
	tokenBudget int
	fbCapacity  int
	logger      *slog.Logger
}

// Option configures an Adapter.
type Option func(*adapterOptions)

// WithPipelineConfig sets the pipeline configuration for local processing.
func WithPipelineConfig(cfg PipelineConfig) Option {
	return func(o *adapterOptions) { o.pipelineCfg = cfg }
}

// WithMemoryStore sets the memory store used by the pipeline.
// If not set, an in-memory store is used (data lost on restart).
func WithMemoryStore(store MemoryStore) Option {
	return func(o *adapterOptions) { o.memStore = store }
}

// WithRefsDir sets the directory where large payloads are offloaded.
// Defaults to <DataDir>/refs.
func WithRefsDir(dir string) Option {
	return func(o *adapterOptions) { o.refsDir = dir }
}

// WithTokenBudget sets the maximum character length of context returned by
// Recall. Recalled text is trimmed to this budget if exceeded. 0 = no limit.
func WithTokenBudget(chars int) Option {
	return func(o *adapterOptions) { o.tokenBudget = chars }
}

// WithMaxRecallTokens sets the maximum number of LLM tokens allowed in a
// recall response. The adapter trims the context to fit this budget using a
// conservative 4-chars-per-token heuristic.
func WithMaxRecallTokens(tokens int) Option {
	return func(o *adapterOptions) { o.tokenBudget = tokens * 4 }
}

// WithFallbackCapacity sets the ring-buffer capacity for the in-process
// fallback cache. Defaults to 1000 entries.
func WithFallbackCapacity(n int) Option {
	return func(o *adapterOptions) { o.fbCapacity = n }
}

// WithLogger sets the structured logger used by the adapter.
// Defaults to a JSON handler writing to os.Stdout at DEBUG level.
func WithLogger(l *slog.Logger) Option {
	return func(o *adapterOptions) { o.logger = l }
}

// NewAdapter wraps store with the local 4-tier memory pipeline.
//
// The adapter processes conversation turns through L0→L3 in-process using
// an OpenAI-compatible LLM endpoint configured via environment variables
// (OPENAI_BASE_URL, OPENAI_API_KEY, OPENAI_MODEL) or via WithPipelineConfig.
func NewAdapter[S any](store session.Store[S], opts ...Option) *Adapter[S] {
	o := &adapterOptions{
		pipelineCfg: PipelineConfigFromEnv(),
		tokenBudget: defaultTokenBudget,
		fbCapacity:  defaultFallbackCapacity,
	}
	for _, opt := range opts {
		opt(o)
	}

	if o.memStore == nil {
		o.memStore = NewInMemoryStore()
	}

	if o.refsDir == "" {
		o.refsDir = filepath.Join(o.pipelineCfg.DataDir, "refs")
	}

	if o.logger == nil {
		o.logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	pipeline := NewPipelineManager(o.pipelineCfg, o.memStore, o.logger)

	return &Adapter[S]{
		store:    store,
		pipeline: pipeline,
		fb:       newFallbackCache(o.fbCapacity),
		off:      newOffloader(o.refsDir, o.logger),
		log:      o.logger,
		budget:   o.tokenBudget,
	}
}

// Get retrieves session data by ID from the underlying store.
func (a *Adapter[S]) Get(ctx context.Context, sessionID string) (*session.Data[S], error) {
	return a.store.Get(ctx, sessionID)
}

// Save persists session data to the underlying store.
// It does NOT automatically fire L0 capture — call [Adapter.Capture] explicitly
// after each conversation turn to feed the pipeline.
func (a *Adapter[S]) Save(ctx context.Context, sessionID string, data *session.Data[S]) error {
	return a.store.Save(ctx, sessionID, data)
}

// Delete removes session data from the underlying store.
// It is a no-op if the underlying store does not support deletion.
func (a *Adapter[S]) Delete(ctx context.Context, sessionID string) error {
	type deleter interface {
		Delete(context.Context, string) error
	}
	if d, ok := a.store.(deleter); ok {
		return d.Delete(ctx, sessionID)
	}
	return nil
}

// Close flushes pending pipeline work and releases resources.
func (a *Adapter[S]) Close() error {
	if err := a.pipeline.Close(); err != nil {
		a.log.Warn("pipeline close error", slog.String("err", err.Error()))
	}

	type closer interface {
		Close() error
	}
	if c, ok := a.store.(closer); ok {
		return c.Close()
	}
	return nil
}

// Capture processes a conversation turn through the local pipeline (L0→L3).
//
// userMsg and assistantMsg are sanitized (role validation, UTF-8 repair,
// zero-delimiter guard) before processing. Large messages exceeding 50 KB are
// offloaded to refs/*.md and replaced with path pointers.
//
// If pipeline processing fails, the turn is buffered in the in-process
// fallback ring buffer and a non-fatal error is returned.
func (a *Adapter[S]) Capture(ctx context.Context, sessionID, userMsg, assistantMsg string) error {
	// Sanitize inputs.
	userMsg, err := SanitizeContent(userMsg)
	if err != nil {
		return fmt.Errorf("memory.Capture: sanitize user: %w", err)
	}
	assistantMsg, err = SanitizeContent(assistantMsg)
	if err != nil {
		return fmt.Errorf("memory.Capture: sanitize assistant: %w", err)
	}

	// Offload large payloads.
	userMsg, err = a.off.MaybeOffload(sessionID, userMsg)
	if err != nil {
		a.log.Warn("offload failed", slog.String("err", err.Error()))
	}
	assistantMsg, err = a.off.MaybeOffload(sessionID, assistantMsg)
	if err != nil {
		a.log.Warn("offload failed", slog.String("err", err.Error()))
	}

	// Build conversation messages for the pipeline.
	now := time.Now()
	messages := []ConversationMessage{
		{
			ID:        generateID(),
			Role:      "user",
			Content:   userMsg,
			Timestamp: now.Add(-time.Millisecond), // user slightly before assistant
			SessionID: sessionID,
		},
		{
			ID:        generateID(),
			Role:      "assistant",
			Content:   assistantMsg,
			Timestamp: now,
			SessionID: sessionID,
		},
	}

	// Feed the local pipeline.
	if err := a.pipeline.Capture(ctx, sessionID, messages); err != nil {
		a.fb.Add(captureEntry{
			SessionKey:       sessionID,
			UserContent:      userMsg,
			AssistantContent: assistantMsg,
			CapturedAt:       now,
		})
		a.log.Warn("pipeline capture failed, buffered",
			slog.String("session", sessionID),
			slog.Int("buffered", a.fb.Len()),
			slog.String("err", err.Error()),
		)
		return fmt.Errorf("memory.Capture: pipeline error (buffered): %w", err)
	}
	return nil
}

// Recall queries the local pipeline for enriched L1–L3 historical context.
//
// The returned string is ready to be prepended to the LLM context. It is
// trimmed to the configured token budget (if any). An empty string is returned
// when no relevant history exists (graceful degradation — the caller's
// generation loop must not be interrupted).
func (a *Adapter[S]) Recall(ctx context.Context, sessionID, query string) (string, error) {
	query, err := SanitizeContent(query)
	if err != nil {
		return "", fmt.Errorf("memory.Recall: sanitize query: %w", err)
	}

	text, err := a.pipeline.Recall(ctx, sessionID, query)
	if err != nil {
		a.log.Warn("recall failed, continuing without context",
			slog.String("session", sessionID),
			slog.String("err", err.Error()),
		)
		return "", nil // graceful degradation
	}

	if a.budget > 0 && len(text) > a.budget {
		text = text[:a.budget]
	}
	return text, nil
}

// EndSession flushes pending pipeline work for the given session.
// Call this when a session is definitively complete (e.g. on logout).
func (a *Adapter[S]) EndSession(_ context.Context, _ string) error {
	// In local mode, pipeline processing is synchronous.
	// Nothing to flush, but keep the API for compatibility.
	return nil
}

// FallbackLen returns the number of capture events currently buffered in the
// fallback ring buffer (i.e. events that could not be processed by the pipeline).
func (a *Adapter[S]) FallbackLen() int {
	return a.fb.Len()
}

// DrainFallback returns all buffered capture events and clears the buffer.
// Use this to re-process events after resolving pipeline issues.
func (a *Adapter[S]) DrainFallback() []captureEntry {
	return a.fb.DrainAll()
}
