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
	numWorkers   int
	maxContext   int
	jobQueue     chan domain.InboundMessageJob
	chatRepo     postgres.ChatRepository
	omnirouteSvc service.OmnirouteService
	gowaSvc      service.GoWAService
	logger       *slog.Logger

	wg       sync.WaitGroup
	isClosed atomic.Bool
	stopOnce sync.Once
}

// NewWorkerPool initializes the background worker pool.
func NewWorkerPool(
	cfg *config.Config,
	chatRepo postgres.ChatRepository,
	omnirouteSvc service.OmnirouteService,
	gowaSvc service.GoWAService,
	logger *slog.Logger,
) WorkerPool {
	return &workerPool{
		numWorkers:   cfg.WorkerPoolSize,
		maxContext:   cfg.MaxContextMessages,
		jobQueue:     make(chan domain.InboundMessageJob, cfg.WorkerQueueSize),
		chatRepo:     chatRepo,
		omnirouteSvc: omnirouteSvc,
		gowaSvc:      gowaSvc,
		logger:       logger,
	}
}

// Start launches worker goroutines.
func (p *workerPool) Start() {
	p.logger.Info("Starting worker pool",
		"workers", p.numWorkers,
		"queue_capacity", cap(p.jobQueue),
		"max_context", p.maxContext,
	)

	for i := 1; i <= p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Enqueue adds an incoming message job to the queue. Returns false if closed or queue is full.
func (p *workerPool) Enqueue(job domain.InboundMessageJob) bool {
	if p.isClosed.Load() {
		p.logger.Warn("Worker pool is closed, discarding incoming message", "sender", job.PhoneNumber)
		return false
	}

	select {
	case p.jobQueue <- job:
		return true
	default:
		p.logger.Error("Worker pool queue is full, dropped message",
			"sender", job.PhoneNumber,
			"queue_length", len(p.jobQueue),
		)
		return false
	}
}

// Stop closes the queue and waits for all active jobs to complete.
func (p *workerPool) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		p.isClosed.Store(true)
		close(p.jobQueue)
		p.logger.Info("Worker pool incoming queue closed, draining in-flight jobs...")
	})

	doneChan := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(doneChan)
	}()

	select {
	case <-doneChan:
		p.logger.Info("All worker pool tasks completed cleanly")
		return nil
	case <-ctx.Done():
		p.logger.Warn("Worker pool shutdown timed out before tasks completed")
		return ctx.Err()
	}
}

// worker loops over jobs from the queue.
func (p *workerPool) worker(id int) {
	defer p.wg.Done()
	p.logger.Debug("Worker started", "worker_id", id)

	for job := range p.jobQueue {
		p.processJob(id, job)
	}

	p.logger.Debug("Worker terminated", "worker_id", id)
}

// processJob executes the AI pipeline for a single user message.
func (p *workerPool) processJob(workerID int, job domain.InboundMessageJob) {
	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	p.logger.Info("Worker processing inbound message",
		"worker_id", workerID,
		"sender", job.PhoneNumber,
	)

	// Step a & b: Save incoming user message to PostgreSQL
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
		// Continue execution even if DB save fails to not break AI reply
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

	// Exclude the current user message from history slice if present
	// to prevent duplicating the prompt in the context payload
	historyContext := make([]domain.ChatMessage, 0, len(recentMsgs))
	for _, m := range recentMsgs {
		if userMsg.ID > 0 && m.ID == userMsg.ID {
			continue
		}
		historyContext = append(historyContext, m)
	}

	// Step d & e: Send prompt and context to Omniroute AI API
	aiReplyRaw, err := p.omnirouteSvc.GenerateResponse(ctx, historyContext, job.Content)
	if err != nil {
		p.logger.Error("Failed to generate AI response from Omniroute",
			"sender", job.PhoneNumber,
			"error", err,
		)

		errorMessage := "Mohon maaf, saat ini sistem AI kami sedang mengalami kendala teknis. Silakan coba kembali dalam beberapa saat."
		_ = p.gowaSvc.SendMessage(ctx, job.SenderJID, errorMessage)
		return
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
