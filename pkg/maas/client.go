package maas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string // Model-specific inference URL (for completions)
	RegistryURL string // MaaS API base URL (for token exchange and model listing)
	APIKey     string // Bearer token used to obtain a session token
	Model      string
	token      string // Session token obtained via token exchange
	HTTPClient *http.Client
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

type ChatChoice struct {
	Index   int         `json:"index"`
	Message ChatMessage `json:"message"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Choices []ChatChoice `json:"choices"`
}

type ModelDetails struct {
	DisplayName string `json:"displayName"`
}

type ModelEntry struct {
	ID           string       `json:"id"`
	URL          string       `json:"url"`
	Ready        bool         `json:"ready"`
	OwnedBy      string       `json:"owned_by"`
	ModelDetails ModelDetails `json:"modelDetails"`
}

type ModelsResponse struct {
	Data []ModelEntry `json:"data"`
}

// ModelInfo is the enriched model data returned to the frontend.
type ModelInfo struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	DisplayName string `json:"display_name"`
	Ready       bool   `json:"ready"`
	OwnedBy     string `json:"owned_by"`
}

func NewClient(baseURL, registryURL, apiKey, model string) *Client {
	return &Client{
		BaseURL:     baseURL,
		RegistryURL: registryURL,
		APIKey:      apiKey,
		Model:       model,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Authenticate exchanges the bearer token for a long-lived session token.
// POST {BaseURL}/v1/tokens with the bearer token to get a session token.
func (c *Client) Authenticate() error {
	if c.token != "" {
		return nil // already authenticated
	}
	if c.APIKey == "" {
		return fmt.Errorf("no bearer token configured")
	}

	tokenURL := strings.TrimRight(c.RegistryURL, "/") + "/v1/tokens"
	payload := []byte(`{"expiration":"720h"}`)

	httpReq, err := http.NewRequest("POST", tokenURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("unmarshal token response: %w", err)
	}
	if tokenResp.Token == "" {
		return fmt.Errorf("empty token in response")
	}

	c.token = tokenResp.Token
	return nil
}

// Complete sends a chat completion request.
// BaseURL should be the model-specific URL (e.g. http://maas.example.com/prelude-maas/llama-32-3b)
// which serves /v1/chat/completions.
func (c *Client) Complete(messages []ChatMessage, systemPrompt string) (string, error) {
	allMessages := make([]ChatMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		allMessages = append(allMessages, ChatMessage{Role: "system", Content: systemPrompt})
	}
	allMessages = append(allMessages, messages...)

	// The inference endpoint expects the model name as the last path segment of the URL,
	// not the registry ID (e.g. "llama-32-3b" not "RedHatAI/llama-3.2-3b-instruct").
	model := c.Model
	trimmed := strings.TrimRight(c.BaseURL, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		model = trimmed[idx+1:]
	}

	req := ChatRequest{
		Model:       model,
		Messages:    allMessages,
		Temperature: 0.7,
		Stream:      false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// The model URL already points to the model serving endpoint.
	// Append /v1/chat/completions if the URL doesn't already contain /v1/.
	completionsURL := strings.TrimRight(c.BaseURL, "/")
	if !strings.Contains(completionsURL, "/v1") {
		completionsURL += "/v1/chat/completions"
	} else {
		completionsURL += "/chat/completions"
	}

	// Authenticate to get a session token
	if err := c.Authenticate(); err != nil {
		return "", fmt.Errorf("authenticate: %w", err)
	}

	httpReq, err := http.NewRequest("POST", completionsURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// ListModels fetches the model registry and returns enriched model info
// including per-model URLs for inference.
// BaseURL should be the MaaS API base (e.g. https://maas.example.com/maas-api).
// Models are fetched from {BaseURL}/v1/models.
func (c *Client) ListModels() ([]ModelInfo, error) {
	// Authenticate to get a session token
	if err := c.Authenticate(); err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	modelsURL := strings.TrimRight(c.RegistryURL, "/") + "/v1/models"
	httpReq, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp ModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	models := make([]ModelInfo, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		if m.ID == "" {
			continue
		}
		displayName := m.ModelDetails.DisplayName
		if displayName == "" {
			displayName = m.ID
		}
		models = append(models, ModelInfo{
			ID:          m.ID,
			URL:         m.URL,
			DisplayName: displayName,
			Ready:       m.Ready,
			OwnedBy:     m.OwnedBy,
		})
	}
	return models, nil
}

// GetToken returns the session token obtained via Authenticate().
func (c *Client) GetToken() string {
	return c.token
}

func (c *Client) HealthCheck() error {
	_, err := c.ListModels()
	return err
}
