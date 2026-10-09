package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/domain"
	"omniroute-api-wa/pkg/textutil"
)

// GoWAService handles the WhatsApp connection lifecycle, QR pairing, and message routing.
type GoWAService interface {
	Connect(ctx context.Context) error
	Disconnect()
	GetClient() *whatsmeow.Client
	GetStatus() domain.WhatsAppStatus
	GetLatestQR() (string, time.Time, bool)
	GetLatestQRPNG(size int) ([]byte, error)
	SendMessage(ctx context.Context, to types.JID, text string) error
	SetInboundMessageHandler(handler func(job domain.InboundMessageJob))
}

type goWAService struct {
	cfg            *config.Config
	client         *whatsmeow.Client
	container      *sqlstore.Container
	logger         *slog.Logger
	inboundHandler func(job domain.InboundMessageJob)

	mu          sync.RWMutex
	latestQR    string
	lastQRTime  time.Time
	hasActiveQR bool
	qrCancel    context.CancelFunc
}

// NewGoWAService creates and configures the WhatsApp service with persistent session storage.
func NewGoWAService(ctx context.Context, cfg *config.Config, logger *slog.Logger) (GoWAService, error) {
	waLogger := waLog.Stdout("WhatsApp", "INFO", true)

	var container *sqlstore.Container
	var err error

	if cfg.WhatsAppSessionStore == "sqlite" {
		dbPath := fmt.Sprintf("file:%s?_foreign_keys=on", cfg.WhatsAppSQLitePath)
		db, errOpen := sql.Open("sqlite", dbPath)
		if errOpen != nil {
			return nil, fmt.Errorf("failed to open sqlite database for whatsmeow: %w", errOpen)
		}
		container = sqlstore.NewWithDB(db, "sqlite3", waLogger)
	} else {
		// Default to PostgreSQL session store
		container, err = sqlstore.New(ctx, "postgres", cfg.PostgresDSN(), waLogger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize whatsmeow postgres container: %w", err)
		}
	}

	// Upgrade/migrate the session database schema
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("failed to upgrade whatsmeow session container: %w", err)
	}

	// Retrieve first device or create a new session device
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve device from session store: %w", err)
	}
	if device == nil {
		device = container.NewDevice()
	}

	client := whatsmeow.NewClient(device, waLogger)

	svc := &goWAService{
		cfg:       cfg,
		client:    client,
		container: container,
		logger:    logger,
	}

	// Register event handlers
	client.AddEventHandler(svc.handleWhatsAppEvent)

	return svc, nil
}

// SetInboundMessageHandler sets the callback for processing incoming user messages.
func (s *goWAService) SetInboundMessageHandler(handler func(job domain.InboundMessageJob)) {
	s.inboundHandler = handler
}

// Connect initiates the WhatsApp socket connection and listens for QR codes if unauthenticated.
func (s *goWAService) Connect(ctx context.Context) error {
	if s.client.Store.ID == nil {
		// Device is not paired yet; listen for QR channel before connecting
		qrCtx, cancel := context.WithCancel(context.Background())
		s.qrCancel = cancel

		qrChan, err := s.client.GetQRChannel(qrCtx)
		if err != nil {
			return fmt.Errorf("failed to get qr channel: %w", err)
		}

		err = s.client.Connect()
		if err != nil {
			cancel()
			return fmt.Errorf("failed to connect whatsapp client: %w", err)
		}

		// Background listener for pairing QR codes
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					s.mu.Lock()
					s.latestQR = evt.Code
					s.lastQRTime = time.Now()
					s.hasActiveQR = true
					s.mu.Unlock()

					fmt.Println()
					fmt.Println("========================================================")
					fmt.Println("             WHATSAPP PAIRING QR CODE                   ")
					fmt.Println("========================================================")
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
					fmt.Println()
					fmt.Println("Scan the QR code above with WhatsApp to authenticate.")
					fmt.Printf("Or visit http://localhost:%s/qr on your browser.\n", s.cfg.Port)
					fmt.Println("========================================================")
					fmt.Println()
				} else {
					s.logger.Info("WhatsApp QR pairing event", "event", evt.Event)
					if evt.Event == "success" {
						s.mu.Lock()
						s.hasActiveQR = false
						s.latestQR = ""
						s.mu.Unlock()
					}
				}
			}
		}()
	} else {
		// Device is already paired, connect directly
		err := s.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect existing whatsapp session: %w", err)
		}
		s.logger.Info("WhatsApp client connected with existing persistent session", "jid", s.client.Store.ID.String())
	}

	return nil
}

// Disconnect cleanly closes WhatsApp connection and session container.
func (s *goWAService) Disconnect() {
	if s.qrCancel != nil {
		s.qrCancel()
	}
	if s.client != nil {
		s.client.Disconnect()
	}
	if s.container != nil {
		_ = s.container.Close()
	}
	s.logger.Info("WhatsApp service disconnected")
}

// GetClient returns the underlying whatsmeow client.
func (s *goWAService) GetClient() *whatsmeow.Client {
	return s.client
}

// GetStatus returns the current connection and login status.
func (s *goWAService) GetStatus() domain.WhatsAppStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := domain.WhatsAppStatus{
		IsConnected:     s.client.IsConnected(),
		IsLoggedIn:      s.client.IsLoggedIn(),
		HasActiveQR:     s.hasActiveQR,
		LastQRGenerated: s.lastQRTime,
	}

	if s.client.Store.ID != nil {
		status.JID = s.client.Store.ID.String()
		status.PhoneNumber = s.client.Store.ID.User
		status.PushName = s.client.Store.PushName
	}

	return status
}

// GetLatestQR returns the raw text of the current QR code.
func (s *goWAService) GetLatestQR() (string, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestQR, s.lastQRTime, s.hasActiveQR
}

// GetLatestQRPNG generates a PNG byte slice for the active QR code.
func (s *goWAService) GetLatestQRPNG(size int) ([]byte, error) {
	s.mu.RLock()
	qr := s.latestQR
	active := s.hasActiveQR
	s.mu.RUnlock()

	if !active || qr == "" {
		return nil, fmt.Errorf("no active qr code available")
	}

	if size <= 0 {
		size = 256
	}

	return qrcode.Encode(qr, qrcode.Medium, size)
}

// SendMessage sends a text message to the specified recipient JID, chunking if necessary.
func (s *goWAService) SendMessage(ctx context.Context, to types.JID, text string) error {
	chunks := textutil.ChunkText(text, textutil.DefaultMaxChunkSize)
	if len(chunks) == 0 {
		return nil
	}

	for i, chunk := range chunks {
		if i > 0 {
			// Small pacing pause between multiple chunks to ensure in-order delivery
			time.Sleep(300 * time.Millisecond)
		}

		msg := &waE2E.Message{
			Conversation: proto.String(chunk),
		}

		_, err := s.client.SendMessage(ctx, to, msg)
		if err != nil {
			return fmt.Errorf("failed to send chunk %d to %s: %w", i+1, to.String(), err)
		}
	}

	return nil
}

// whatsAppLinkedDeviceServer is the server identifier for newer WhatsApp accounts
// using the Linked Device ID (LID) scheme, distinct from the classic s.whatsapp.net.
const whatsAppLinkedDeviceServer = "lid"

// handleWhatsAppEvent dispatches incoming events.
func (s *goWAService) handleWhatsAppEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		// 1. Ignore messages sent by self
		if v.Info.IsFromMe {
			return
		}

		// 2. Only process private direct messages.
		// Supports both classic (s.whatsapp.net) and modern LID (lid) server schemes.
		isPrivateChat := !v.Info.IsGroup &&
			(v.Info.Chat.Server == types.DefaultUserServer || v.Info.Chat.Server == whatsAppLinkedDeviceServer)
		if !isPrivateChat {
			return
		}

		// 3. Extract text content and detect media
		text := extractMessageText(v.Message)
		hasMedia := v.Message.GetImageMessage() != nil ||
			v.Message.GetVideoMessage() != nil ||
			v.Message.GetDocumentMessage() != nil ||
			v.Message.GetStickerMessage() != nil
		
		// Skip if both text and media are empty
		if strings.TrimSpace(text) == "" && !hasMedia {
			return
		}
		
		// Add image indicator if image sent without caption
		if hasMedia && strings.TrimSpace(text) == "" {
			text = "[IMAGE ATTACHMENT]"
		}

		// 4. Extract sender info
		senderJID := v.Info.Chat
		phoneNumber := v.Info.Sender.User
		if phoneNumber == "" {
			phoneNumber = v.Info.Chat.User
		}

		s.logger.Info("Received incoming private message",
			"phone_number", phoneNumber,
			"length", len(text),
		)

		// 5. Extract and download media attachments
		mediaAttachments := s.extractAndDownloadMedia(v.Message)

		// 6. Dispatch to background worker pool
		if s.inboundHandler != nil {
			s.inboundHandler(domain.InboundMessageJob{
				SenderJID:        senderJID,
				PhoneNumber:      phoneNumber,
				Content:          text,
				MediaAttachments: mediaAttachments,
				ReceivedAt:       v.Info.Timestamp,
			})
		}

	case *events.Connected:
		s.logger.Info("WhatsApp connection established")

	case *events.Disconnected:
		s.logger.Warn("WhatsApp connection dropped")

	case *events.LoggedOut:
		s.logger.Warn("WhatsApp session was logged out by user or server")
		s.mu.Lock()
		s.hasActiveQR = false
		s.latestQR = ""
		s.mu.Unlock()
	}
}

// extractMessageText extracts textual content from supported WhatsApp message types.
func extractMessageText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}

	// Unwrap Ephemeral (Disappearing) Messages
	if msg.EphemeralMessage != nil && msg.EphemeralMessage.Message != nil {
		return extractMessageText(msg.EphemeralMessage.Message)
	}

	// Unwrap View Once Messages
	if msg.ViewOnceMessage != nil && msg.ViewOnceMessage.Message != nil {
		return extractMessageText(msg.ViewOnceMessage.Message)
	}

	// Unwrap View Once V2 Messages
	if msg.ViewOnceMessageV2 != nil && msg.ViewOnceMessageV2.Message != nil {
		return extractMessageText(msg.ViewOnceMessageV2.Message)
	}

	if text := msg.GetConversation(); text != "" {
		return text
	}

	if ext := msg.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText()
	}

	if img := msg.GetImageMessage(); img != nil && img.GetCaption() != "" {
		return img.GetCaption()
	}

	if doc := msg.GetDocumentMessage(); doc != nil && doc.GetCaption() != "" {
		return doc.GetCaption()
	}

	if vid := msg.GetVideoMessage(); vid != nil && vid.GetCaption() != "" {
		return vid.GetCaption()
	}

	return ""
}

// extractAndDownloadMedia extracts all media from a WhatsApp message,
// downloads each attachment via whatsmeow, and encodes as base64 data URL.
// Falls back to direct URL if download fails.
func (s *goWAService) extractAndDownloadMedia(msg *waE2E.Message) []domain.MediaAttachment {
	var attachments []domain.MediaAttachment

	if msg == nil {
		return attachments
	}

	// Unwrap Ephemeral Messages
	if msg.EphemeralMessage != nil && msg.EphemeralMessage.Message != nil {
		return s.extractAndDownloadMedia(msg.EphemeralMessage.Message)
	}

	// Unwrap View Once Messages
	if msg.ViewOnceMessage != nil && msg.ViewOnceMessage.Message != nil {
		return s.extractAndDownloadMedia(msg.ViewOnceMessage.Message)
	}

	// Unwrap View Once V2 Messages
	if msg.ViewOnceMessageV2 != nil && msg.ViewOnceMessageV2.Message != nil {
		return s.extractAndDownloadMedia(msg.ViewOnceMessageV2.Message)
	}

	// Extract Image
	if img := msg.GetImageMessage(); img != nil {
		mimeType := "image/jpeg"
		if img.GetMimetype() != "" {
			mimeType = img.GetMimetype()
		}
		fileName := fmt.Sprintf("image_%d.jpg", time.Now().UnixNano())
		att := domain.MediaAttachment{
			Type:      domain.MediaTypeImage,
			URL:       img.GetURL(),
			MimeType:  mimeType,
			FileName:  fileName,
			Size:      int64(img.GetFileLength()),
			CreatedAt: time.Now(),
		}
		att.Base64Data = s.downloadAsBase64(img, mimeType)
		s.logger.Info("Extracted image attachment",
			"url_len", len(att.URL),
			"has_base64", att.Base64Data != "",
			"size", att.Size,
		)
		attachments = append(attachments, att)
	}

	// Extract Video
	if vid := msg.GetVideoMessage(); vid != nil {
		mimeType := "video/mp4"
		if vid.GetMimetype() != "" {
			mimeType = vid.GetMimetype()
		}
		fileName := fmt.Sprintf("video_%d.mp4", time.Now().UnixNano())
		att := domain.MediaAttachment{
			Type:      domain.MediaTypeVideo,
			URL:       vid.GetURL(),
			MimeType:  mimeType,
			FileName:  fileName,
			Size:      int64(vid.GetFileLength()),
			CreatedAt: time.Now(),
		}
		att.Base64Data = s.downloadAsBase64(vid, mimeType)
		attachments = append(attachments, att)
	}

	// Extract Document
	if doc := msg.GetDocumentMessage(); doc != nil {
		mimeType := "application/octet-stream"
		if doc.GetMimetype() != "" {
			mimeType = doc.GetMimetype()
		}
		fileName := doc.GetFileName()
		if fileName == "" {
			fileName = doc.GetTitle()
		}
		if fileName == "" {
			fileName = fmt.Sprintf("document_%d", time.Now().UnixNano())
		}
		att := domain.MediaAttachment{
			Type:      domain.MediaTypeDocument,
			URL:       doc.GetURL(),
			MimeType:  mimeType,
			FileName:  fileName,
			Size:      int64(doc.GetFileLength()),
			CreatedAt: time.Now(),
		}
		att.Base64Data = s.downloadAsBase64(doc, mimeType)
		attachments = append(attachments, att)
	}

	// Extract Sticker
	if sticker := msg.GetStickerMessage(); sticker != nil {
		mimeType := "image/webp"
		if sticker.GetMimetype() != "" {
			mimeType = sticker.GetMimetype()
		}
		fileName := fmt.Sprintf("sticker_%d.webp", time.Now().UnixNano())
		att := domain.MediaAttachment{
			Type:      domain.MediaTypeSticker,
			URL:       sticker.GetURL(),
			MimeType:  mimeType,
			FileName:  fileName,
			Size:      int64(sticker.GetFileLength()),
			CreatedAt: time.Now(),
		}
		att.Base64Data = s.downloadAsBase64(sticker, mimeType)
		attachments = append(attachments, att)
	}

	return attachments
}

// downloadAsBase64 downloads a WhatsApp media message via whatsmeow and returns
// a base64 data URL string (data:<mime>;base64,<data>).
// Returns empty string on failure so caller can fallback to direct URL.
func (s *goWAService) downloadAsBase64(msg whatsmeow.DownloadableMessage, mimeType string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := s.client.Download(ctx, msg)
	if err != nil {
		s.logger.Warn("Failed to download media via whatsmeow, will use direct URL",
			"error", err,
			"mime_type", mimeType,
		)
		return ""
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
}
