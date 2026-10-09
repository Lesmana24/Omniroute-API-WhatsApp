package worker

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/domain"
	"omniroute-api-wa/internal/repository/postgres"
	"omniroute-api-wa/internal/service"
	"omniroute-api-wa/pkg/textutil"
)

// WorkerPool manages asynchronous concurrent handling of inbound WhatsApp messages.
type WorkerPool interface {
	Start()
	Enqueue(job domain.InboundMessageJob) bool
	Stop(ctx context.Context) error
}

type workerPool struct {
	numWorkers         int
	maxContext         int
	enableMultimodal   bool
	maxMediaSizeMB     int
	jobQueue           chan domain.InboundMessageJob
	chatRepo           postgres.ChatRepository
	mediaRepo          postgres.MediaRepository
	omnirouteSvc       service.OmnirouteService
	gowaSvc            service.GoWAService
	logger             *slog.Logger

	wg       sync.WaitGroup
	isClosed atomic.Bool
	stopOnce sync.Once
}

// NewWorkerPool initializes the background worker pool with media support.
func NewWorkerPool(
	cfg *config.Config,
	chatRepo postgres.ChatRepository,
	mediaRepo postgres.MediaRepository,
	omnirouteSvc service.OmnirouteService,
	gowaSvc service.GoWAService,
	logger *slog.Logger,
) WorkerPool {
	return &workerPool{
		numWorkers:       cfg.WorkerPoolSize,
		maxContext:       cfg.MaxContextMessages,
		enableMultimodal: cfg.EnableMultimodalAI,
		maxMediaSizeMB:   cfg.MaxMediaSizeMB,
		jobQueue:         make(chan domain.InboundMessageJob, cfg.WorkerQueueSize),
		chatRepo:         chatRepo,
		mediaRepo:        mediaRepo,
		omnirouteSvc:     omnirouteSvc,
		gowaSvc:          gowaSvc,
		logger:           logger,
	}
}

// Start launches worker goroutines.
func (p *workerPool) Start() {
	p.logger.Info("Starting worker pool",
		"workers", p.numWorkers,
		"queue_capacity", cap(p.jobQueue),
		"max_context", p.maxContext,
		"enable_multimodal", p.enableMultimodal,
	)

	for i := 1; i <= p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Enqueue adds an incoming WhatsApp job to the worker queue.
func (p *workerPool) Enqueue(job domain.InboundMessageJob) bool {
	if p.isClosed.Load() {
		return false
	}

	select {
	case p.jobQueue <- job:
		return true
	default:
		return false
	}
}

// Stop waits for pending messages to flush and gracefully terminates workers.
func (p *workerPool) Stop(ctx context.Context) error {
	var stopErr error
	p.stopOnce.Do(func() {
		p.isClosed.Store(true)
		close(p.jobQueue)

		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			p.logger.Info("All background workers stopped gracefully")
		case <-ctx.Done():
			stopErr = ctx.Err()
			p.logger.Warn("Worker pool shutdown timed out with lingering jobs", "error", stopErr)
		}
	})

	return stopErr
}

// worker routine pulling jobs from the internal channel.
func (p *workerPool) worker(id int) {
	defer p.wg.Done()

	for job := range p.jobQueue {
		p.processJob(id, job)
	}

	p.logger.Debug("Worker terminated", "worker_id", id)
}

// processJob executes the AI pipeline for a single user message (text and/or media).
func (p *workerPool) processJob(workerID int, job domain.InboundMessageJob) {
	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	p.logger.Info("Worker processing inbound message",
		"worker_id", workerID,
		"sender", job.PhoneNumber,
		"has_media", len(job.MediaAttachments) > 0,
		"media_count", len(job.MediaAttachments),
	)

	// Step a: Save incoming user message to PostgreSQL
	userMsg := &domain.ChatMessage{
		PhoneNumber: job.PhoneNumber,
		Role:        domain.RoleUser,
		Content:     job.Content,
	}

	if err := p.chatRepo.Save(ctx, userMsg); err != nil {
		p.logger.Error("Failed to save incoming user message to DB",
			"sender", job.PhoneNumber,
			"error", err,
		)
	}

	// Step b: If media attachments exist, save them and link to chatMessage
	var validMedia []domain.MediaAttachment
	if len(job.MediaAttachments) > 0 && userMsg.ID > 0 {
		maxSizeBytes := int64(p.maxMediaSizeMB) * 1024 * 1024
		for _, media := range job.MediaAttachments {
			// Validate size constraint (5MB limit)
			if maxSizeBytes > 0 && media.Size > maxSizeBytes {
				p.logger.Warn("Media attachment exceeds size limit, skipping",
					"size", media.Size,
					"max_bytes", maxSizeBytes,
				)
				continue
			}

			media.ChatMessageID = userMsg.ID
			media.CreatedAt = time.Now()
			if p.mediaRepo != nil {
				if err := p.mediaRepo.SaveMediaAttachment(&media); err != nil {
					p.logger.Error("Failed to save media attachment to DB",
						"chat_message_id", userMsg.ID,
						"error", err,
					)
				}
			}
			validMedia = append(validMedia, media)
		}
	}

	// Step c: Fetch recent N messages from PostgreSQL as context
	recentMsgs, err := p.chatRepo.GetRecentContext(ctx, job.PhoneNumber, p.maxContext)
	if err != nil {
		p.logger.Error("Failed to fetch conversation context from DB",
			"sender", job.PhoneNumber,
			"error", err,
		)
		recentMsgs = nil
	}

	// Exclude current message from history context
	historyContext := make([]domain.ChatMessage, 0, len(recentMsgs))
	for _, m := range recentMsgs {
		if userMsg.ID > 0 && m.ID == userMsg.ID {
			continue
		}
		historyContext = append(historyContext, m)
	}

	// Step d & e: Send prompt to Omniroute AI API (multimodal if media present)
	var aiReplyRaw string
	if p.enableMultimodal && len(validMedia) > 0 {
		p.logger.Info("Dispatching multimodal request to Omniroute AI",
			"sender", job.PhoneNumber,
			"image_count", len(validMedia),
		)
		aiReplyRaw, err = p.omnirouteSvc.GenerateResponseWithMedia(ctx, historyContext, job.Content, validMedia)
	} else {
		aiReplyRaw, err = p.omnirouteSvc.GenerateResponse(ctx, historyContext, job.Content)
	}

	if err != nil {
		p.logger.Error("Failed to generate AI response from Omniroute",
			"sender", job.PhoneNumber,
			"error", err,
		)

		// Generic fallback response
		errorMessage := "Mohon maaf, saat ini sistem AI kami sedang mengalami kendala teknis. Silakan coba kembali dalam beberapa saat."
		_ = p.gowaSvc.SendMessage(ctx, job.SenderJID, errorMessage)
		return
	}

	// Mark media as processed
	if p.mediaRepo != nil && len(validMedia) > 0 {
		now := time.Now()
		for _, m := range validMedia {
			if m.ID > 0 {
				_ = p.mediaRepo.UpdateMediaProcessed(m.ID, &now)
			}
		}
	}

	// Apply WhatsApp formatting
	aiReply := textutil.FormatWhatsAppText(aiReplyRaw)

	// Step f & g: Save AI assistant reply to PostgreSQL
	aiMsg := &domain.ChatMessage{
		PhoneNumber: job.PhoneNumber,
		Role:        domain.RoleAssistant,
		Content:     aiReply,
	}

	if err := p.chatRepo.Save(ctx, aiMsg); err != nil {
		p.logger.Error("Failed to save AI response to DB",
			"sender", job.PhoneNumber,
			"error", err,
		)
	}

	// Step h: Send AI reply to user via WhatsApp (chunks automatically if > 3800 chars)
	if err := p.gowaSvc.SendMessage(ctx, job.SenderJID, aiReply); err != nil {
		p.logger.Error("Failed to send WhatsApp reply to user",
			"sender", job.PhoneNumber,
			"error", err,
		)
		return
	}

	p.logger.Info("Successfully processed and replied to user message",
		"worker_id", workerID,
		"sender", job.PhoneNumber,
		"reply_length", len(aiReply),
		"duration", time.Since(startTime).String(),
	)
}
