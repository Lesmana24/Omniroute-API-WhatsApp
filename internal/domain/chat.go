package domain

import (
	"time"

	"go.mau.fi/whatsmeow/types"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

type MediaType string

const (
	MediaTypeImage      MediaType = "image"
	MediaTypeVideo      MediaType = "video"
	MediaTypeDocument   MediaType = "document"
	MediaTypeSticker    MediaType = "sticker"
)

type MediaAttachment struct {
	ID            int64      `json:"id"`
	ChatMessageID int64      `json:"chat_message_id"`
	Type          MediaType  `json:"type"`
	URL           string     `json:"url"`
	MimeType      string     `json:"mime_type"`
	FileName      string     `json:"file_name"`
	Size          int64      `json:"size"`
	Base64Data    string     `json:"base64_data,omitempty"` // in-memory only, not persisted
	ProcessedAt   *time.Time `json:"processed_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ChatMessage represents a single message record in the chat_histories table.
type ChatMessage struct {
	ID               int64     `json:"id"`
	PhoneNumber      string    `json:"phone_number"`
	Role             string    `json:"role"` // "user", "assistant", or "system"
	Content          string    `json:"content"`
	MediaAttachmentIDs []int64 `json:"media_attachment_ids"`
	CreatedAt        time.Time `json:"created_at"`
}

// OmnirouteMessage represents a single chat turn in the Omniroute AI payload.
// Content can be string or []OmnirouteContentPart (for multimodal).
type OmnirouteMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// OmnirouteContentPart represents a part of a multimodal content message.
type OmnirouteContentPart struct {
	Type     string             `json:"type"` // "text" or "image_url"
	Text     string             `json:"text,omitempty"`
	ImageURL *OmnirouteImageURL `json:"image_url,omitempty"`
}

// OmnirouteImageURL holds the image URL or base64 data for vision models.
type OmnirouteImageURL struct {
	URL string `json:"url"`
}

// OmnirouteChatRequest is the payload sent to Omniroute AI chat completions.
type OmnirouteChatRequest struct {
	Model       string             `json:"model"`
	Messages    []OmnirouteMessage `json:"messages"`
	Temperature float64            `json:"temperature,omitempty"`
}

// OmnirouteChatChoice represents choice response item from OpenAI-compatible gateways.
type OmnirouteChatChoice struct {
	Index        int              `json:"index"`
	Message      OmnirouteMessage `json:"message"`
	FinishReason string           `json:"finish_reason,omitempty"`
}

// OmnirouteChatResponse is the response structure parsed from Omniroute AI.
type OmnirouteChatResponse struct {
	ID      string                `json:"id,omitempty"`
	Object  string                `json:"object,omitempty"`
	Created int64                 `json:"created,omitempty"`
	Model   string                `json:"model,omitempty"`
	Choices []OmnirouteChatChoice `json:"choices,omitempty"`

	// Direct response field fallbacks if gateway returns flat responses
	Response string `json:"response,omitempty"`
	Reply    string `json:"reply,omitempty"`
	Text     string `json:"text,omitempty"`
}

// WhatsAppStatus represents the current status of the WhatsApp client.
type WhatsAppStatus struct {
	IsConnected     bool      `json:"is_connected"`
	IsLoggedIn      bool      `json:"is_logged_in"`
	JID             string    `json:"jid,omitempty"`
	PhoneNumber     string    `json:"phone_number,omitempty"`
	PushName        string    `json:"push_name,omitempty"`
	HasActiveQR     bool      `json:"has_active_qr"`
	LastQRGenerated time.Time `json:"last_qr_generated,omitempty"`
}

// InboundMessageJob represents a task dispatched to the asynchronous worker pool.
type InboundMessageJob struct {
	SenderJID          types.JID
	PhoneNumber        string
	Content            string
	MediaAttachments   []MediaAttachment
	ReceivedAt         time.Time
}
