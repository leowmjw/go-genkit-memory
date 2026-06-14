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

// defaultGatewayURL is used when MEMORY_TENCENTDB_GATEWAY_HOST/PORT are not set
// via options.
const defaultGatewayURL = "http://127.0.0.1:8420"

// defaultTokenBudget is the maximum number of characters returned by Recall.
// 0 means no limit.
const defaultTokenBudget = 0

// Adapter wraps any [session.Store] and layers the TencentDB memory gateway
// on top for long-term memory. It is safe for concurrent use.
//
// The adapter implements [session.Store] so it is a drop-in replacement
// wherever a store is accepted (session.New, session.Load, etc.).
//
// Additionally it exposes:
//   - [Adapter.Capture]: asynchronous L0 turn capture sent to the gateway.
//   - [Adapter.Recall]: synchronous L1–L3 context retrieval from the gateway.
//   - [Adapter.EndSession]: flush pending pipeline work on session end.
type Adapter[S any] struct {
	store   session.Store[S]
	client  *gatewayClient
	fb      *fallbackCache
	off     *offloader
	log     *slog.Logger
	budget  int
}

// adapterOptions collects functional options.
type adapterOptions struct {
	gatewayURL  string
	apiKey      string
	refsDir     string
	tokenBudget int
	fbCapacity  int
	logger      *slog.Logger
}

// Option configures an Adapter.
type Option func(*adapterOptions)

// WithGatewayURL sets the base URL of the TencentDB memory gateway.
// Defaults to http://127.0.0.1:8420.
func WithGatewayURL(url string) Option {
	return func(o *adapterOptions) { o.gatewayURL = url }
}

// WithAPIKey sets the bearer token used to authenticate with the gateway.
// Leave empty for unauthenticated local deployments.
func WithAPIKey(key string) Option {
	return func(o *adapterOptions) { o.apiKey = key }
}

// WithRefsDir sets the directory where large payloads are offloaded.
// Defaults to ~/.memory-tencentdb/memory-tdai/refs.
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

// NewAdapter wraps store with the TencentDB memory adapter.
//
// The adapter reads MEMORY_TENCENTDB_GATEWAY_HOST and
// MEMORY_TENCENTDB_GATEWAY_PORT from the environment to locate the gateway,
// and MEMORY_TENCENTDB_GATEWAY_API_KEY for auth. These can be overridden with
// options.
func NewAdapter[S any](store session.Store[S], opts ...Option) *Adapter[S] {
	o := &adapterOptions{
		tokenBudget: defaultTokenBudget,
		fbCapacity:  defaultFallbackCapacity,
	}
	for _, opt := range opts {
		opt(o)
	}

	// Resolve gateway URL from env if not set via option.
	if o.gatewayURL == "" {
		host := os.Getenv("MEMORY_TENCENTDB_GATEWAY_HOST")
		port := os.Getenv("MEMORY_TENCENTDB_GATEWAY_PORT")
		if host == "" {
			host = "127.0.0.1"
		}
		if port == "" {
			port = "8420"
		}
		o.gatewayURL = fmt.Sprintf("http://%s:%s", host, port)
	}

	if o.apiKey == "" {
		o.apiKey = os.Getenv("MEMORY_TENCENTDB_GATEWAY_API_KEY")
	}

	if o.refsDir == "" {
		home, _ := os.UserHomeDir()
		o.refsDir = filepath.Join(home, ".memory-tencentdb", "memory-tdai", "refs")
	}

	if o.logger == nil {
		o.logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	return &Adapter[S]{
		store:  store,
		client: newGatewayClient(o.gatewayURL, o.apiKey, o.logger),
		fb:     newFallbackCache(o.fbCapacity),
		off:    newOffloader(o.refsDir, o.logger),
		log:    o.logger,
		budget: o.tokenBudget,
	}
}

// Get retrieves session data by ID from the underlying store.
func (a *Adapter[S]) Get(ctx context.Context, sessionID string) (*session.Data[S], error) {
	return a.store.Get(ctx, sessionID)
}

// Save persists session data to the underlying store.
// It does NOT automatically fire L0 capture — call [Adapter.Capture] explicitly
// after each conversation turn to route data to the gateway.
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
	// Best-effort: flush all sessions with buffered captures.
	drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.client.EndSession(drainCtx, "")

	type closer interface {
		Close() error
	}
	if c, ok := a.store.(closer); ok {
		return c.Close()
	}
	return nil
}

// Capture asynchronously sends a conversation turn to the gateway for L0
// capture and subsequent pipeline processing (L1→L3).
//
// userMsg and assistantMsg are sanitized (role validation, UTF-8 repair,
// zero-delimiter guard) before dispatch. Large messages exceeding 50 KB are
// offloaded to refs/*.md and replaced with path pointers.
//
// If the gateway is unavailable the turn is buffered in the in-process
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

	req := CaptureRequest{
		SessionKey:       sessionID,
		UserContent:      userMsg,
		AssistantContent: assistantMsg,
	}

	onFailure := func(err error) {
		a.fb.Add(captureEntry{
			SessionKey:       sessionID,
			UserContent:      req.UserContent,
			AssistantContent: req.AssistantContent,
			CapturedAt:       time.Now(),
		})
		a.log.Warn("capture queued to fallback buffer",
			slog.String("session", sessionID),
			slog.Int("buffered", a.fb.Len()),
			slog.String("err", err.Error()),
		)
	}

	if err := a.client.Capture(ctx, req, onFailure); err != nil {
		return fmt.Errorf("memory.Capture: gateway unavailable (buffered): %w", err)
	}
	return nil
}

// Recall queries the gateway for enriched L1–L3 historical context.
//
// The returned string is ready to be prepended to the LLM context. It is
// trimmed to the configured token budget (if any). An empty string is returned
// when no relevant history exists or the gateway is unavailable (graceful
// degradation — the caller's generation loop must not be interrupted).
func (a *Adapter[S]) Recall(ctx context.Context, sessionID, query string) (string, error) {
	query, err := SanitizeContent(query)
	if err != nil {
		return "", fmt.Errorf("memory.Recall: sanitize query: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, gatewayTimeout)
	defer cancel()

	text, err := a.client.Recall(ctx, RecallRequest{
		SessionKey: sessionID,
		Query:      query,
	})
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
func (a *Adapter[S]) EndSession(ctx context.Context, sessionID string) error {
	return a.client.EndSession(ctx, sessionID)
}

// FallbackLen returns the number of capture events currently buffered in the
// fallback ring buffer (i.e. events that could not reach the gateway).
func (a *Adapter[S]) FallbackLen() int {
	return a.fb.Len()
}

// DrainFallback returns all buffered capture events and clears the buffer.
// Use this to re-deliver events once the gateway recovers.
func (a *Adapter[S]) DrainFallback() []captureEntry {
	return a.fb.DrainAll()
}
