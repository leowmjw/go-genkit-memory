package memory

// Layer represents one of the four memory tiers.
type Layer int

const (
	// LayerL0 is raw conversation storage.
	LayerL0 Layer = 0
	// LayerL1 is episodic atom extraction.
	LayerL1 Layer = 1
	// LayerL2 is scene block aggregation.
	LayerL2 Layer = 2
	// LayerL3 is persona synthesis.
	LayerL3 Layer = 3
)

// CaptureRequest is the body sent to POST /capture.
type CaptureRequest struct {
	SessionKey string    `json:"session_key"`
	Messages   []Message `json:"messages"`
}

// Message is a single conversational turn entry.
type Message struct {
	Role    string `json:"role"`    // "user" | "assistant" | "system"
	Content string `json:"content"`
}

// RecallRequest is the body sent to POST /recall.
type RecallRequest struct {
	SessionKey string `json:"session_key"`
	Query      string `json:"query"`
}

// RecallResponse is the response body from POST /recall.
type RecallResponse struct {
	// Context is the enriched historical text to inject before LLM generation.
	Context string `json:"context"`
}

// SessionEndRequest is the body sent to POST /session/end.
type SessionEndRequest struct {
	SessionKey string `json:"session_key"`
}

// HealthResponse is the response body from GET /health.
type HealthResponse struct {
	// Status is "ok" or "degraded".
	Status string `json:"status"`
}
