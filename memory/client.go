package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	gatewayTimeout          = 5 * time.Second
	circuitBreakerThreshold = 5
	circuitBreakerPause     = 60 * time.Second
	captureMaxInflight      = 4
	captureInflightTimeout  = 5 * time.Second
)

// gatewayClient is an HTTP client for the TencentDB memory gateway.
// It enforces a 5-second request timeout, a circuit breaker (5 consecutive
// failures → 60 s pause), and back-pressure (max 4 in-flight captures).
type gatewayClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	log        *slog.Logger

	// circuit breaker
	mu         sync.Mutex
	failures   int
	pauseUntil time.Time

	// back-pressure semaphore: limits concurrent capture goroutines
	captureSem chan struct{}
}

// newGatewayClient constructs a client pointing at baseURL.
func newGatewayClient(baseURL, apiKey string, log *slog.Logger) *gatewayClient {
	return &gatewayClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: gatewayTimeout,
		},
		log:        log,
		captureSem: make(chan struct{}, captureMaxInflight),
	}
}

// isOpen returns true when the circuit breaker is open (requests are paused).
func (c *gatewayClient) isOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failures >= circuitBreakerThreshold {
		if time.Now().Before(c.pauseUntil) {
			return true
		}
		// Pause window expired — reset.
		c.failures = 0
	}
	return false
}

// recordSuccess resets the circuit-breaker failure counter.
func (c *gatewayClient) recordSuccess() {
	c.mu.Lock()
	c.failures = 0
	c.mu.Unlock()
}

// recordFailure increments the failure counter and may arm the circuit breaker.
func (c *gatewayClient) recordFailure() {
	c.mu.Lock()
	c.failures++
	if c.failures >= circuitBreakerThreshold {
		c.pauseUntil = time.Now().Add(circuitBreakerPause)
	}
	c.mu.Unlock()
}

// post sends a JSON-encoded body to the given path and decodes the response into v.
func (c *gatewayClient) post(ctx context.Context, path string, body, v any) error {
	start := time.Now()

	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("memory: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("memory: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.recordFailure()
		return fmt.Errorf("memory: POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		c.recordFailure()
		return fmt.Errorf("memory: POST %s: HTTP %d: %s", path, resp.StatusCode, body)
	}

	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			c.recordFailure()
			return fmt.Errorf("memory: decode response from %s: %w", path, err)
		}
	}

	c.recordSuccess()
	c.log.Debug("gateway call",
		slog.String("path", path),
		slog.Duration("latency", time.Since(start)),
	)
	return nil
}

// Capture fires an async L0 capture for the given session turn.
// It respects the back-pressure semaphore (max 4 in-flight).
// Returns immediately; the actual HTTP call runs in a goroutine.
// If the semaphore is full, the oldest slot is waited on for up to 5 s.
// onFailure is called from the goroutine if the HTTP call fails; it may be nil.
func (c *gatewayClient) Capture(ctx context.Context, req CaptureRequest, onFailure func(error)) error {
	if c.isOpen() {
		err := fmt.Errorf("memory: circuit breaker open — skipping capture")
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}

	// Back-pressure: wait for a slot, but cap the wait.
	acquireCtx, cancel := context.WithTimeout(ctx, captureInflightTimeout)
	defer cancel()
	select {
	case c.captureSem <- struct{}{}:
	case <-acquireCtx.Done():
		err := fmt.Errorf("memory: capture back-pressure timeout: %w", acquireCtx.Err())
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}

	go func() {
		defer func() { <-c.captureSem }()
		// Use a fresh background context; the caller's context may expire.
		bgCtx, bgCancel := context.WithTimeout(context.Background(), gatewayTimeout)
		defer bgCancel()
		if err := c.post(bgCtx, "/capture", req, nil); err != nil {
			c.log.Warn("L0 capture failed", slog.String("err", err.Error()))
			if onFailure != nil {
				onFailure(err)
			}
		}
	}()
	return nil
}

// Recall queries the gateway for enriched L1–L3 context.
func (c *gatewayClient) Recall(ctx context.Context, req RecallRequest) (string, error) {
	if c.isOpen() {
		return "", fmt.Errorf("memory: circuit breaker open — skipping recall")
	}
	var resp RecallResponse
	if err := c.post(ctx, "/recall", req, &resp); err != nil {
		return "", err
	}
	return resp.Context, nil
}

// EndSession flushes pending pipeline work for a session.
func (c *gatewayClient) EndSession(ctx context.Context, sessionKey string) error {
	if c.isOpen() {
		return nil // best-effort; skip if breaker open
	}
	return c.post(ctx, "/session/end", SessionEndRequest{SessionKey: sessionKey}, nil)
}

// Health checks if the gateway is responsive.
func (c *gatewayClient) Health(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return "", fmt.Errorf("memory: health check: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("memory: health check: %w", err)
	}
	defer resp.Body.Close()
	var h HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return "", fmt.Errorf("memory: decode health: %w", err)
	}
	return h.Status, nil
}
