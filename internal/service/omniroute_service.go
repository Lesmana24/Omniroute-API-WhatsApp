package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/domain"
)

// OmnirouteService defines the client interface to interact with Omniroute AI API.
type OmnirouteService interface {
	GenerateResponse(ctx context.Context, history []domain.ChatMessage, currentPrompt string) (string, error)
}

type omnirouteService struct {
	client   *http.Client
	baseURL  string
	apiKey   string
	model    string
	endpoint string
}

// NewOmnirouteService creates a new HTTP client for Omniroute AI.
func NewOmnirouteService(cfg *config.Config) OmnirouteService {
	baseURL := strings.TrimRight(cfg.OmnirouteAPIBaseURL, "/")
	endpoint := baseURL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = baseURL + "/chat/completions"
	}

	transport := &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	return &omnirouteService{
		client: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		},
		baseURL:  baseURL,
		apiKey:   cfg.OmnirouteAPIKey,
		model:    cfg.OmnirouteModel,
		endpoint: endpoint,
	}
}

// GenerateResponse builds the prompt context with conversation history and queries Omniroute AI.
func (s *omnirouteService) GenerateResponse(ctx context.Context, history []domain.ChatMessage, currentPrompt string) (string, error) {
	messages := make([]domain.OmnirouteMessage, 0, len(history)+2)

	// System instruction
	messages = append(messages, domain.OmnirouteMessage{
		Role:    domain.RoleSystem,
		Content: "Anda adalah asisten virtual WhatsApp AI yang cerdas, ramah, dan solutif. Jawablah pesan dengan jelas, ringkas, dan relevan menggunakan bahasa yang sopan.",
	})

	// Append previous conversation context
	for _, h := range history {
		if strings.TrimSpace(h.Content) == "" {
			continue
		}
		role := h.Role
		if role != domain.RoleUser && role != domain.RoleAssistant && role != domain.RoleSystem {
			role = domain.RoleUser
		}
		messages = append(messages, domain.OmnirouteMessage{
			Role:    role,
			Content: h.Content,
		})
	}

	// Append current prompt
	messages = append(messages, domain.OmnirouteMessage{
		Role:    domain.RoleUser,
		Content: currentPrompt,
	})

	reqBody := domain.OmnirouteChatRequest{
		Model:       s.model,
		Messages:    messages,
		Temperature: 0.7,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal omniroute request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		req.Header.Set("X-API-Key", s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to dispatch request to omniroute: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read omniroute response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("omniroute api returned error HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var chatResp domain.OmnirouteChatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", fmt.Errorf("failed to decode omniroute response json: %w", err)
	}

	// 1. Check standard OpenAI format choices[0].message.content
	if len(chatResp.Choices) > 0 && chatResp.Choices[0].Message.Content != "" {
		return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
	}

	// 2. Fallbacks for non-standard gateway response shapes
	if chatResp.Response != "" {
		return strings.TrimSpace(chatResp.Response), nil
	}
	if chatResp.Reply != "" {
		return strings.TrimSpace(chatResp.Reply), nil
	}
	if chatResp.Text != "" {
		return strings.TrimSpace(chatResp.Text), nil
	}

	return "", fmt.Errorf("omniroute returned empty completion choices")
}
