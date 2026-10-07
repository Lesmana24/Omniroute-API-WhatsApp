package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"omniroute-api-wa/internal/config"
	deliveryHttp "omniroute-api-wa/internal/delivery/http"
	"omniroute-api-wa/internal/domain"
	"omniroute-api-wa/internal/repository/postgres"
	"omniroute-api-wa/internal/service"
	"omniroute-api-wa/internal/worker"
)

func main() {
	// 1. Initialize structured logger
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	logger.Info("Starting Omniroute WhatsApp Integration Service...")

	// 2. Load and validate environment configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load application configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("Configuration loaded successfully",
		"port", cfg.Port,
		"gin_mode", cfg.GinMode,
		"db_host", cfg.DBHost,
		"db_name", cfg.DBName,
		"omniroute_base_url", cfg.OmnirouteAPIBaseURL,
		"omniroute_model", cfg.OmnirouteModel,
		"max_context", cfg.MaxContextMessages,
		"session_store", cfg.WhatsAppSessionStore,
	)

	// 3. Set Gin mode
	gin.SetMode(cfg.GinMode)

	// 4. Initialize PostgreSQL connection pool
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pgPool, err := postgres.NewConnectionPool(ctx, cfg)
	if err != nil {
		logger.Error("Failed to initialize PostgreSQL connection pool", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	logger.Info("PostgreSQL connection pool established successfully")

	// 5. Ensure chat_histories table and indexes exist
	if err := postgres.AutoMigrate(ctx, pgPool); err != nil {
		logger.Error("Failed to execute PostgreSQL schema migration", "error", err)
		os.Exit(1)
	}
	logger.Info("PostgreSQL database migrations applied successfully")

	// 6. Initialize Repositories and Services
	chatRepo := postgres.NewChatRepository(pgPool)
	omnirouteSvc := service.NewOmnirouteService(cfg)

	gowaSvc, err := service.NewGoWAService(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("Failed to initialize GoWA / whatsmeow service", "error", err)
		os.Exit(1)
	}

	// 7. Initialize and start asynchronous worker pool
	workerPool := worker.NewWorkerPool(cfg, chatRepo, omnirouteSvc, gowaSvc, logger)
	workerPool.Start()

	// 8. Bind inbound message dispatcher to worker pool
	gowaSvc.SetInboundMessageHandler(func(job domain.InboundMessageJob) {
		enqueued := workerPool.Enqueue(job)
		if !enqueued {
			logger.Warn("Incoming WhatsApp message could not be queued", "sender", job.PhoneNumber)
		}
	})

	// 9. Connect WhatsApp client and listen for QR / events
	if err := gowaSvc.Connect(context.Background()); err != nil {
		logger.Error("Failed to initiate WhatsApp client connection", "error", err)
		os.Exit(1)
	}

	// 10. Setup Gin HTTP router
	router := gin.New()
	router.Use(deliveryHttp.LoggerMiddleware(logger))
	router.Use(deliveryHttp.RecoveryMiddleware(logger))
	router.Use(deliveryHttp.CORSMiddleware())

	handler := deliveryHttp.NewHandler(cfg, chatRepo, gowaSvc)
	handler.RegisterRoutes(router)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 11. Run HTTP server in background goroutine
	go func() {
		logger.Info(fmt.Sprintf("HTTP Server listening on http://localhost:%s", cfg.Port))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP Server fatal error", "error", err)
			os.Exit(1)
		}
	}()

	// 12. Graceful Shutdown orchestration
	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	sig := <-shutdownSignal
	logger.Info("Shutdown signal received, starting graceful termination...", "signal", sig.String())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Step A: Stop HTTP server from accepting new traffic
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown encountered error", "error", err)
	} else {
		logger.Info("HTTP server stopped accepting connections")
	}

	// Step B: Drain and stop worker pool
	if err := workerPool.Stop(shutdownCtx); err != nil {
		logger.Error("Worker pool shutdown error", "error", err)
	}

	// Step C: Disconnect WhatsApp client
	gowaSvc.Disconnect()

	// Step D: Close database pool
	chatRepo.Close()

	logger.Info("Graceful shutdown completed successfully. Process exiting.")
}
