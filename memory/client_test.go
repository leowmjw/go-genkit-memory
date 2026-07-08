package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCallChatCompletion_Success verifies the happy-path HTTP call to a mock LLM.
func TestCallChatCompletion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request.
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("want /v1/chat/completions, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got == "" {
			t.Error("expected Authorization header to be set")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("want application/json, got %s", r.Header.Get("Content-Type"))
		}

		// Decode and verify the request body.
		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("want model test-model, got %s", req.Model)
		}
		if len(req.Messages) != 2 {
			t.Fatalf("want 2 messages, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("want system role, got %s", req.Messages[0].Role)
		}
		if req.Messages[1].Role != "user" {
			t.Errorf("want user role, got %s", req.Messages[1].Role)
		}

		// Return a valid response.
		resp := chatCompletionResponse{
			ID: "chatcmpl-test",
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: `[{"scene_name":"test","memories":[]}]`},
					FinishReason: "stop",
				},
			},
			Usage: struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			}{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := LLMConfig{
		BaseURL: srv.URL + "/v1",
		APIKey:  "test-key",
		Model:   "test-model",
		Timeout: 5 * time.Second,
	}

	result, err := callChatCompletion(context.Background(), cfg, "system prompt", "user prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `[{"scene_name":"test","memories":[]}]` {
		t.Errorf("unexpected result: %q", result)
	}
}

// TestCallChatCompletion_NoBaseURL verifies error when BaseURL is empty.
func TestCallChatCompletion_NoBaseURL(t *testing.T) {
	cfg := LLMConfig{}
	_, err := callChatCompletion(context.Background(), cfg, "sys", "user")
	if err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
}

// TestCallChatCompletion_HTTPError verifies that non-200 responses return errors.
func TestCallChatCompletion_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()

	cfg := LLMConfig{
		BaseURL: srv.URL + "/v1",
		Model:   "test",
		Timeout: 5 * time.Second,
	}

	_, err := callChatCompletion(context.Background(), cfg, "sys", "user")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// TestCallChatCompletion_Timeout verifies that slow responses trigger a timeout.
func TestCallChatCompletion_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := LLMConfig{
		BaseURL: srv.URL + "/v1",
		Model:   "test",
		Timeout: 100 * time.Millisecond,
	}

	_, err := callChatCompletion(context.Background(), cfg, "sys", "user")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// TestCallChatCompletion_EmptyChoices verifies error on empty choices.
func TestCallChatCompletion_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := chatCompletionResponse{ID: "test", Choices: nil}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := LLMConfig{
		BaseURL: srv.URL + "/v1",
		Model:   "test",
		Timeout: 5 * time.Second,
	}

	_, err := callChatCompletion(context.Background(), cfg, "sys", "user")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

// TestCallChatCompletionText_Success verifies the text (non-JSON) variant.
func TestCallChatCompletionText_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify response_format is NOT set (no JSON mode).
		var req chatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ResponseFormat != nil {
			t.Error("expected no response_format for text call")
		}

		resp := chatCompletionResponse{
			ID: "chatcmpl-text",
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: "# User Persona\n\n## Base & Facts\n- Developer"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := LLMConfig{
		BaseURL: srv.URL + "/v1",
		APIKey:  "key",
		Model:   "test",
		Timeout: 5 * time.Second,
	}

	result, err := callChatCompletionText(context.Background(), cfg, "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "# User Persona\n\n## Base & Facts\n- Developer" {
		t.Errorf("unexpected result: %q", result)
	}
}
