package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"omniroute-api-wa/internal"
	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/domain"
)

// ChatRepository defines the storage interface for conversation history.
type ChatRepository interface {
	Save(ctx context.Context, msg *domain.ChatMessage) error
	GetRecentContext(ctx context.Context, phoneNumber string, limit int) ([]domain.ChatMessage, error)
	Ping(ctx context.Context) error
	Close()
}

// chatRepository implements ChatRepository using PostgreSQL pgxpool.
type chatRepository struct {
	pool *pgxpool.Pool
}

// NewConnectionPool creates a new configured PostgreSQL connection pool.
func NewConnectionPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.PostgresDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to parse postgres dsn: %w", err)
	}

	// High performance connection pool settings
	poolConfig.MaxConns = 25
	poolConfig.MinConns = 5
	poolConfig.MaxConnLifetime = 1 * time.Hour
	poolConfig.MaxConnIdleTime = 15 * time.Minute
	poolConfig.HealthCheckPeriod = 1 * time.Minute

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres connection pool: %w", err)
	}

	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping postgres database: %w", err)
	}

	return pool, nil
}

// NewChatRepository initializes the PostgreSQL chat repository.
func NewChatRepository(pool *pgxpool.Pool) ChatRepository {
	return &chatRepository{
		pool: pool,
	}
}

// AutoMigrate creates required tables/columns if not already present.
// Uses ALTER TABLE ... ADD COLUMN IF NOT EXISTS to safely handle existing tables.
func AutoMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	query := `
	CREATE TABLE IF NOT EXISTS chat_histories (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		phone_number VARCHAR(32) NOT NULL,
		role VARCHAR(16) NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
		content TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	ALTER TABLE chat_histories ADD COLUMN IF NOT EXISTS media_attachment_ids BIGINT[];

	CREATE INDEX IF NOT EXISTS idx_chat_histories_phone_created
		ON chat_histories (phone_number, created_at DESC, id DESC);

	CREATE INDEX IF NOT EXISTS idx_chat_histories_created_at
		ON chat_histories (created_at);

	CREATE TABLE IF NOT EXISTS media_attachments (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		chat_message_id BIGINT NOT NULL REFERENCES chat_histories(id) ON DELETE CASCADE,
		type VARCHAR(16) NOT NULL,
		url TEXT NOT NULL,
		mime_type VARCHAR(255) NOT NULL,
		file_name VARCHAR(255),
		size BIGINT,
		processed_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS idx_media_attachments_chat_message
		ON media_attachments (chat_message_id);

	CREATE INDEX IF NOT EXISTS idx_media_attachments_type
		ON media_attachments (type);
	`
	_, err := pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to execute auto-migration: %w", err)
	}
	return nil
}

// Save inserts a new chat message and updates the struct with generated ID and CreatedAt.
func (r *chatRepository) Save(ctx context.Context, msg *domain.ChatMessage) error {
	query := `
		INSERT INTO chat_histories (phone_number, role, content, media_attachment_ids, created_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING id, created_at;
	`

	var mediaIDs []int64
	if len(msg.MediaAttachmentIDs) > 0 {
		mediaIDs = msg.MediaAttachmentIDs
	}

	err := r.pool.QueryRow(ctx, query, msg.PhoneNumber, msg.Role, msg.Content, mediaIDs).Scan(&msg.ID, &msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to save chat message: %w", err)
	}

	return nil
}

// GetRecentContext fetches the last N messages for the specified phone number,
// ordered chronologically ascending so that AI conversation flow remains natural.
func (r *chatRepository) GetRecentContext(ctx context.Context, phoneNumber string, limit int) ([]domain.ChatMessage, error) {
	if limit <= 0 {
		limit = internal.DefaultMaxContextMessages
	}

	query := `
		SELECT id, phone_number, role, content, created_at
		FROM (
			SELECT id, phone_number, role, content, created_at
			FROM chat_histories
			WHERE phone_number = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2
		) sub
		ORDER BY created_at ASC, id ASC;
	`

	rows, err := r.pool.Query(ctx, query, phoneNumber, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query chat context for phone %s: %w", phoneNumber, err)
	}
	defer rows.Close()

	messages := make([]domain.ChatMessage, 0, limit)
	for rows.Next() {
		var m domain.ChatMessage
		if err := rows.Scan(&m.ID, &m.PhoneNumber, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan chat message row: %w", err)
		}
		messages = append(messages, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}

	return messages, nil
}

// Ping checks if the PostgreSQL connection is healthy.
func (r *chatRepository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// Close gracefully terminates the connection pool.
func (r *chatRepository) Close() {
	if r.pool != nil {
		r.pool.Close()
	}
}
