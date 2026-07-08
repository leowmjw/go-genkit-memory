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

// CaptureRequest is the internal request for L0 capture.
type CaptureRequest struct {
	SessionKey       string `json:"session_key"`
	UserContent      string `json:"user_content"`
	AssistantContent string `json:"assistant_content"`
}

// Message is a single conversational turn entry used internally.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant" | "system"
	Content string `json:"content"`
}

// RecallRequest is the internal request for context recall.
type RecallRequest struct {
	SessionKey string `json:"session_key"`
	Query      string `json:"query"`
}

// RecallResponse is the internal response from context recall.
type RecallResponse struct {
	// Context is the enriched historical text to inject before LLM generation.
	Context string `json:"context"`
}
