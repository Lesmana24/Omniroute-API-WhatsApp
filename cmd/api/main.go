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
	"github.com/jackc/pgx/v5/pgxpool"

	"omniroute-api-wa/internal/config"
	deliveryHttp "omniroute-api-wa/internal/delivery/http"
	"omniroute-api-wa/internal/domain"
	"omniroute-api-wa/internal/repository/postgres"
	"omniroute-api-wa/internal/service"
	"omniroute-api-wa/internal/worker"
)

func main() {
	// Initialize logger
	logger := setupLogger()
	logger.Info("Starting Omniroute WhatsApp Integration Service...")

	// Load configuration
	cfg := loadConfiguration(logger)

	// Set Gin mode
	setGinMode(cfg.GinMode)

	// Initialize PostgreSQL connection pool
	pgPool := initializePostgreSQLConnectionPool(cfg, logger)
	defer pgPool.Close()

	// Run database migrations
	runDatabaseMigrations(pgPool, logger)

	// Initialize services
	chatRepo, omnirouteSvc, gowaSvc := initializeServices(cfg, pgPool, logger)

	// Initialize and start worker pool
	workerPool := initializeWorkerPool(cfg, chatRepo, omnirouteSvc, gowaSvc, logger)
	workerPool.Start()

	// Bind inbound message handler
	gowaSvc.SetInboundMessageHandler(func(job domain.InboundMessageJob) {
		enqueued := workerPool.Enqueue(job)
		if !enqueued {
			logger.Warn("Incoming WhatsApp message could not be queued", "sender", job.PhoneNumber)
		}
	})

	// Connect WhatsApp client
	connectWhatsAppClient(gowaSvc, logger)

	// Setup HTTP server
	server := setupHTTPServer(cfg, chatRepo, gowaSvc, logger)

	// Run HTTP server in background goroutine
	go runHTTPServer(server, logger)

	// Handle graceful shutdown
	handleGracefulShutdown(server, workerPool, gowaSvc, chatRepo, logger)
}

// setupLogger initializes the structured logger.
func setupLogger() *slog.Logger {
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)
	return logger
}

// loadConfiguration loads and validates the application configuration.
func loadConfiguration(logger *slog.Logger) *config.Config {
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
	return cfg
}

// setGinMode sets the Gin framework mode.
func setGinMode(ginMode string) {
	gin.SetMode(ginMode)
}

// initializePostgreSQLConnectionPool creates and returns a PostgreSQL connection pool.
func initializePostgreSQLConnectionPool(cfg *config.Config, logger *slog.Logger) *pgxpool.Pool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pgPool, err := postgres.NewConnectionPool(ctx, cfg)
	if err != nil {
		logger.Error("Failed to initialize PostgreSQL connection pool", "error", err)
		os.Exit(1)
	}

	logger.Info("PostgreSQL connection pool established successfully")
	return pgPool
}

// runDatabaseMigrations runs the PostgreSQL schema migrations.
func runDatabaseMigrations(pool *pgxpool.Pool, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := postgres.AutoMigrate(ctx, pool); err != nil {
		logger.Error("Failed to execute PostgreSQL schema migration", "error", err)
		os.Exit(1)
	}
	logger.Info("PostgreSQL database migrations applied successfully")
}

// initializeServices initializes the repositories and services.
func initializeServices(cfg *config.Config, pgPool *pgxpool.Pool, logger *slog.Logger) (
	postgres.ChatRepository,
	service.OmnirouteService,
	service.GoWAService,
) {
	chatRepo := postgres.NewChatRepository(pgPool)
	omnirouteSvc := service.NewOmnirouteService(cfg)

	gowaSvc, err := service.NewGoWAService(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("Failed to initialize GoWA / whatsmeow service", "error", err)
		os.Exit(1)
	}

	return chatRepo, omnirouteSvc, gowaSvc
}

// initializeWorkerPool initializes and returns the worker pool.
func initializeWorkerPool(
	cfg *config.Config,
	chatRepo postgres.ChatRepository,
	omnirouteSvc service.OmnirouteService,
	gowaSvc service.GoWAService,
	logger *slog.Logger,
) worker.WorkerPool {
	workerPool := worker.NewWorkerPool(cfg, chatRepo, omnirouteSvc, gowaSvc, logger)
	workerPool.Start()
	return workerPool
}

// setupHTTPServer sets up and returns the HTTP server.
func setupHTTPServer(
	cfg *config.Config,
	chatRepo postgres.ChatRepository,
	gowaSvc service.GoWAService,
	logger *slog.Logger,
) *http.Server {
	// Setup Gin HTTP router
	router := gin.New()
	router.Use(deliveryHttp.LoggerMiddleware(logger))
	router.Use(deliveryHttp.RecoveryMiddleware(logger))
	router.Use(deliveryHttp.CORSMiddleware())

	handler := deliveryHttp.NewHandler(cfg, chatRepo, gowaSvc)
	handler.RegisterRoutes(router)

	return &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// runHTTPServer runs the HTTP server in a goroutine.
func runHTTPServer(server *http.Server, logger *slog.Logger) {
	go func() {
		logger.Info(fmt.Sprintf("HTTP Server listening on http://localhost:%s", server.Addr[1:])) // Skip the ':' in Addr
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP Server fatal error", "error", err)
			os.Exit(1)
		}
	}()
}

// handleGracefulShutdown handles the graceful shutdown of the application.
func handleGracefulShutdown(
	server *http.Server,
	workerPool worker.WorkerPool,
	gowaSvc service.GoWAService,
	chatRepo postgres.ChatRepository,
	logger *slog.Logger,
) {
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

// connectWhatsAppClient connects the WhatsApp client and listens for QR / events.
func connectWhatsAppClient(gowaSvc service.GoWAService, logger *slog.Logger) {
	if err := gowaSvc.Connect(context.Background()); err != nil {
		logger.Error("Failed to initiate WhatsApp client connection", "error", err)
		os.Exit(1)
	}
}
