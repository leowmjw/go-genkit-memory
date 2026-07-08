package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// chatCompletionRequest is the OpenAI-compatible chat completions request body.
type chatCompletionRequest struct {
	Model       string              `json:"model"`
	Messages    []chatMessage       `json:"messages"`
	Temperature float64             `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Stream      bool                `json:"stream"`
	ResponseFormat *responseFormat  `json:"response_format,omitempty"`
}

// responseFormat requests a specific output format from the LLM.
type responseFormat struct {
	Type string `json:"type"` // "json_object" for JSON mode
}

// chatMessage is a single message in the chat completion request.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionResponse is the OpenAI-compatible chat completions response.
type chatCompletionResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// callChatCompletion calls an OpenAI-compatible chat completions endpoint.
// It sends the system and user prompts as messages and returns the assistant's
// response content. The function respects context cancellation and applies the
// configured timeout.
//
// This is the underlying implementation used by all LLM function variable seams
// (callLLMExtract, callLLMDedup, callLLMScene, callLLMPersona).
func callChatCompletion(ctx context.Context, cfg LLMConfig, systemPrompt, userPrompt string) (string, error) {
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("client: LLM BaseURL is not configured")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	reqBody := chatCompletionRequest{
		Model: cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.1, // low temperature for deterministic extraction
		Stream:      false,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("client: marshal request: %w", err)
	}

	httpCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := cfg.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("client: request failed: %w", err)
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("client: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("client: decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("client: empty response (no choices)")
	}

	content := result.Choices[0].Message.Content

	slog.Debug("LLM call complete",
		slog.String("model", cfg.Model),
		slog.Duration("latency", latency),
		slog.Int("prompt_tokens", result.Usage.PromptTokens),
		slog.Int("completion_tokens", result.Usage.CompletionTokens),
	)

	return content, nil
}

// callChatCompletionText is like callChatCompletion but does not request JSON
// response format. Used for L3 persona generation which outputs markdown.
func callChatCompletionText(ctx context.Context, cfg LLMConfig, systemPrompt, userPrompt string) (string, error) {
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("client: LLM BaseURL is not configured")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	reqBody := chatCompletionRequest{
		Model: cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.3,
		Stream:      false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("client: marshal request: %w", err)
	}

	httpCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := cfg.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("client: request failed: %w", err)
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("client: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("client: decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("client: empty response (no choices)")
	}

	content := result.Choices[0].Message.Content

	slog.Debug("LLM call complete",
		slog.String("model", cfg.Model),
		slog.Duration("latency", latency),
		slog.Int("prompt_tokens", result.Usage.PromptTokens),
		slog.Int("completion_tokens", result.Usage.CompletionTokens),
	)

	return content, nil
}
