package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"omniroute-api-wa/internal/domain"
)

// MediaRepository defines the storage interface for media attachments.
type MediaRepository interface {
	SaveMediaAttachment(media *domain.MediaAttachment) error
	GetMediaAttachmentByID(id int64) (*domain.MediaAttachment, error)
	GetMediaAttachmentsByChatMessageID(chatMessageID int64) ([]domain.MediaAttachment, error)
	UpdateMediaProcessed(id int64, processedAt *time.Time) error
	DeleteMediaAttachment(id int64) error
}

type mediaRepository struct {
	pool *pgxpool.Pool
}

// NewMediaRepository initializes the PostgreSQL media repository.
func NewMediaRepository(pool *pgxpool.Pool) MediaRepository {
	return &mediaRepository{pool: pool}
}

// SaveMediaAttachment inserts a new media attachment and populates its generated fields.
func (r *mediaRepository) SaveMediaAttachment(media *domain.MediaAttachment) error {
	query := `
		INSERT INTO media_attachments (chat_message_id, type, url, mime_type, file_name, size, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		RETURNING id, processed_at, created_at
	`

	err := r.pool.QueryRow(
		context.Background(),
		query,
		media.ChatMessageID,
		string(media.Type),
		media.URL,
		media.MimeType,
		media.FileName,
		media.Size,
	).Scan(
		&media.ID,
		&media.ProcessedAt,
		&media.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save media attachment: %w", err)
	}

	return nil
}

// GetMediaAttachmentByID retrieves a single media attachment by its primary key.
func (r *mediaRepository) GetMediaAttachmentByID(id int64) (*domain.MediaAttachment, error) {
	query := `
		SELECT id, chat_message_id, type, url, mime_type, file_name, size, processed_at, created_at
		FROM media_attachments
		WHERE id = $1
	`

	var media domain.MediaAttachment
	var typeStr string

	err := r.pool.QueryRow(context.Background(), query, id).Scan(
		&media.ID,
		&media.ChatMessageID,
		&typeStr,
		&media.URL,
		&media.MimeType,
		&media.FileName,
		&media.Size,
		&media.ProcessedAt,
		&media.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get media attachment %d: %w", id, err)
	}

	media.Type = domain.MediaType(typeStr)
	return &media, nil
}

// GetMediaAttachmentsByChatMessageID retrieves all media attachments for a chat message.
func (r *mediaRepository) GetMediaAttachmentsByChatMessageID(chatMessageID int64) ([]domain.MediaAttachment, error) {
	query := `
		SELECT id, chat_message_id, type, url, mime_type, file_name, size, processed_at, created_at
		FROM media_attachments
		WHERE chat_message_id = $1
		ORDER BY created_at ASC
	`

	rows, err := r.pool.Query(context.Background(), query, chatMessageID)
	if err != nil {
		return nil, fmt.Errorf("failed to query media attachments for message %d: %w", chatMessageID, err)
	}
	defer rows.Close()

	var mediaList []domain.MediaAttachment
	for rows.Next() {
		var media domain.MediaAttachment
		var typeStr string

		if err := rows.Scan(
			&media.ID,
			&media.ChatMessageID,
			&typeStr,
			&media.URL,
			&media.MimeType,
			&media.FileName,
			&media.Size,
			&media.ProcessedAt,
			&media.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan media attachment row: %w", err)
		}

		media.Type = domain.MediaType(typeStr)
		mediaList = append(mediaList, media)
	}

	return mediaList, rows.Err()
}

// UpdateMediaProcessed marks a media attachment as processed with the given timestamp.
func (r *mediaRepository) UpdateMediaProcessed(id int64, processedAt *time.Time) error {
	query := `
		UPDATE media_attachments
		SET processed_at = $1
		WHERE id = $2
	`

	tag, err := r.pool.Exec(context.Background(), query, processedAt, id)
	if err != nil {
		return fmt.Errorf("failed to update media processed timestamp for id %d: %w", id, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("media attachment %d not found", id)
	}

	return nil
}

// DeleteMediaAttachment removes a media attachment record by ID.
func (r *mediaRepository) DeleteMediaAttachment(id int64) error {
	query := `DELETE FROM media_attachments WHERE id = $1`

	tag, err := r.pool.Exec(context.Background(), query, id)
	if err != nil {
		return fmt.Errorf("failed to delete media attachment %d: %w", id, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("media attachment %d not found", id)
	}

	return nil
}
