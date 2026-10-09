package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/domain"
)

// OmnirouteService defines the client interface to interact with Omniroute AI API.
type OmnirouteService interface {
	GenerateResponse(ctx context.Context, history []domain.ChatMessage, currentPrompt string) (string, error)
	GenerateResponseWithMedia(ctx context.Context, history []domain.ChatMessage, currentPrompt string, media []domain.MediaAttachment) (string, error)
}

type omnirouteService struct {
	client   *http.Client
	baseURL  string
	apiKey   string
	model    string
	endpoint string
	logger   *slog.Logger
}

// NewOmnirouteService creates a new HTTP client for Omniroute AI.
func NewOmnirouteService(cfg *config.Config, logger *slog.Logger) OmnirouteService {
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
			Timeout:   120 * time.Second, // allow longer timeout for multimodal vision models
		},
		baseURL:  baseURL,
		apiKey:   cfg.OmnirouteAPIKey,
		model:    cfg.OmnirouteModel,
		endpoint: endpoint,
		logger:   logger,
	}
}

// buildSystemMessage returns standard system instruction prompt.
func buildSystemMessage() domain.OmnirouteMessage {
	return domain.OmnirouteMessage{
		Role: domain.RoleSystem,
		Content: "Anda adalah asisten virtual WhatsApp AI yang cerdas, ramah, dan solutif. Jawablah pesan dengan jelas, ringkas, dan relevan menggunakan bahasa yang sopan.\n\nKEMAMPUAN MULTIMODAL:\n- Jika pengguna mengirimkan gambar, foto, screenshot, atau dokumen, Anda BISA melihat dan menganalisis isinya secara langsung.\n- JANGAN PERNAH mengatakan Anda tidak bisa melihat gambar atau meminta pengguna mengirim ulang jika gambar sudah terlampir di input.\n- Analisis teks, objek, soal, kode, screenshot game, dan konten lain yang ada di dalam gambar secara detail dan berikan jawaban yang akurat.\n\nPENTING: Gunakan HANYA format teks WhatsApp:\n- Bold: *teks*\n- Italic: _teks_\n- Strikethrough: ~teks~\n- Monospace: `teks`\n- List: gunakan tanda peluru (•) atau angka (1. 2. 3.)\n- JANGAN gunakan format Markdown standar seperti ###, ##, ---, atau **bold**.\n- Gunakan baris kosong untuk memisahkan paragraf agar mudah dibaca di WhatsApp.",
	}
}

// appendHistory converts domain history to OmnirouteMessage slice.
func appendHistory(messages []domain.OmnirouteMessage, history []domain.ChatMessage) []domain.OmnirouteMessage {
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
	return messages
}

// GenerateResponse builds text-only prompt context and queries Omniroute AI.
func (s *omnirouteService) GenerateResponse(ctx context.Context, history []domain.ChatMessage, currentPrompt string) (string, error) {
	messages := make([]domain.OmnirouteMessage, 0, len(history)+2)
	messages = append(messages, buildSystemMessage())
	messages = appendHistory(messages, history)

	// Append current user prompt as simple string
	messages = append(messages, domain.OmnirouteMessage{
		Role:    domain.RoleUser,
		Content: currentPrompt,
	})

	return s.doChatCompletion(ctx, messages)
}

// GenerateResponseWithMedia builds multimodal prompt context with image/media attachments.
func (s *omnirouteService) GenerateResponseWithMedia(
	ctx context.Context,
	history []domain.ChatMessage,
	currentPrompt string,
	media []domain.MediaAttachment,
) (string, error) {
	// If no media provided, fallback directly to text-only call
	if len(media) == 0 {
		return s.GenerateResponse(ctx, history, currentPrompt)
	}

	// Filter for image attachments only for vision AI
	var imageAttachments []domain.MediaAttachment
	for _, m := range media {
		if m.Type == domain.MediaTypeImage || strings.HasPrefix(m.MimeType, "image/") {
			imageAttachments = append(imageAttachments, m)
		}
	}

	if len(imageAttachments) == 0 {
		return s.GenerateResponse(ctx, history, currentPrompt)
	}

	messages := make([]domain.OmnirouteMessage, 0, len(history)+2)
	messages = append(messages, buildSystemMessage())
	messages = appendHistory(messages, history)

	// Construct multimodal content parts array (OpenAI Vision standard format)
	contentParts := make([]domain.OmnirouteContentPart, 0, len(imageAttachments)+1)

	// Add text part first if available
	promptText := currentPrompt
	if strings.TrimSpace(promptText) == "" || promptText == "[IMAGE ATTACHMENT]" {
		promptText = "Tolong analisis dan jelaskan gambar ini secara detail."
	}
	contentParts = append(contentParts, domain.OmnirouteContentPart{
		Type: "text",
		Text: promptText,
	})

	// Add all image attachments as multimodal image_url parts.
	// Priority: base64 data URL (downloaded via whatsmeow) > direct CDN URL.
	for _, img := range imageAttachments {
		imageURL := img.Base64Data // prefer base64 — CDN URLs require WhatsApp auth
		if imageURL == "" {
			imageURL = img.URL // fallback to direct URL
		}
		if imageURL == "" {
			continue // skip if neither available
		}
		contentParts = append(contentParts, domain.OmnirouteContentPart{
			Type: "image_url",
			ImageURL: &domain.OmnirouteImageURL{
				URL: imageURL,
			},
		})
	}

	messages = append(messages, domain.OmnirouteMessage{
		Role:    domain.RoleUser,
		Content: contentParts,
	})

	// Try multimodal first; if it fails, fallback to text-only with generic annotation
	reply, err := s.doChatCompletion(ctx, messages)
	if err != nil {
		// Fallback: retry with text-only prompt describing image attachment
		fallbackPrompt := fmt.Sprintf("%s\n\n[Catatan: Pengguna melampirkan %d gambar]", currentPrompt, len(imageAttachments))
		return s.GenerateResponse(ctx, history, fallbackPrompt)
	}

	return reply, nil
}

// doChatCompletion executes the HTTP request against Omniroute chat completions API.
func (s *omnirouteService) doChatCompletion(ctx context.Context, messages []domain.OmnirouteMessage) (string, error) {
	reqBody := domain.OmnirouteChatRequest{
		Model:       s.model,
		Messages:    messages,
		Temperature: 0.7,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal omniroute request: %w", err)
	}

	if s.logger != nil {
		s.logger.Info("Sending request to Omniroute AI",
			"endpoint", s.endpoint,
			"model", s.model,
			"payload_size_bytes", len(jsonBytes),
			"message_count", len(messages),
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request to omniroute failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if s.logger != nil {
		s.logger.Info("Received response from Omniroute AI",
			"status_code", resp.StatusCode,
			"response_len", len(bodyBytes),
		)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("omniroute api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp domain.OmnirouteChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return "", fmt.Errorf("failed to decode omniroute response json: %w", err)
	}

	if s.logger != nil {
		s.logger.Info("Parsed Omniroute AI response",
			"model_used", chatResp.Model,
			"choices_count", len(chatResp.Choices),
		)
	}

	// 1. Standard OpenAI response extraction
	if len(chatResp.Choices) > 0 {
		msgContent := chatResp.Choices[0].Message.Content
		switch v := msgContent.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v), nil
			}
		case []interface{}:
			// In case choice returns content parts
			var sb strings.Builder
			for _, part := range v {
				if m, ok := part.(map[string]interface{}); ok {
					if text, ok := m["text"].(string); ok {
						sb.WriteString(text)
					}
				}
			}
			if sb.Len() > 0 {
				return strings.TrimSpace(sb.String()), nil
			}
		}
	}

	// 2. Direct flat response fallbacks
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
