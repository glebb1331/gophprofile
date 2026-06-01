package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gophprofile/avatars-service/internal/domain"
)

type AvatarRepository struct {
	pool *pgxpool.Pool
}

func NewAvatarRepository(pool *pgxpool.Pool) *AvatarRepository {
	return &AvatarRepository{pool: pool}
}

func (r *AvatarRepository) Create(ctx context.Context, a *domain.Avatar) error {
	thumbsJSON, err := json.Marshal(a.ThumbnailKeys)
	if err != nil {
		return fmt.Errorf("marshal thumbnails: %w", err)
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	const q = `
INSERT INTO avatars (id, user_id, file_name, mime_type, size_bytes, width, height, s3_key, thumbnail_s3_keys, upload_status, processing_status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q,
		a.ID, a.UserID, a.FileName, a.MimeType, a.SizeBytes,
		a.Width, a.Height, a.S3Key, thumbsJSON,
		a.UploadStatus, a.ProcessingStatus,
	).Scan(&a.CreatedAt, &a.UpdatedAt)
}

func (r *AvatarRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Avatar, error) {
	const q = `
SELECT id, user_id, file_name, mime_type, size_bytes, width, height, s3_key, thumbnail_s3_keys,
       upload_status, processing_status, created_at, updated_at, deleted_at
FROM avatars
WHERE id = $1 AND deleted_at IS NULL`
	row := r.pool.QueryRow(ctx, q, id)
	return scanAvatar(row)
}

func (r *AvatarRepository) GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	const q = `
SELECT id, user_id, file_name, mime_type, size_bytes, width, height, s3_key, thumbnail_s3_keys,
       upload_status, processing_status, created_at, updated_at, deleted_at
FROM avatars
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1`
	row := r.pool.QueryRow(ctx, q, userID)
	return scanAvatar(row)
}

func (r *AvatarRepository) ListByUserID(ctx context.Context, userID string) ([]*domain.Avatar, error) {
	const q = `
SELECT id, user_id, file_name, mime_type, size_bytes, width, height, s3_key, thumbnail_s3_keys,
       upload_status, processing_status, created_at, updated_at, deleted_at
FROM avatars
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*domain.Avatar
	for rows.Next() {
		a, err := scanAvatar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *AvatarRepository) UpdateProcessingResult(ctx context.Context, id uuid.UUID, thumbs domain.ThumbnailKeys, status domain.ProcessingStatus, width, height int) error {
	thumbsJSON, err := json.Marshal(thumbs)
	if err != nil {
		return fmt.Errorf("marshal thumbnails: %w", err)
	}
	const q = `
UPDATE avatars
SET thumbnail_s3_keys = $2, processing_status = $3, width = $4, height = $5, updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL`
	ct, err := r.pool.Exec(ctx, q, id, thumbsJSON, status, width, height)
	if err != nil {
		return fmt.Errorf("update processing: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrAvatarNotFound
	}
	return nil
}

func (r *AvatarRepository) UpdateProcessingStatus(ctx context.Context, id uuid.UUID, status domain.ProcessingStatus) error {
	const q = `UPDATE avatars SET processing_status = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`
	ct, err := r.pool.Exec(ctx, q, id, status)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrAvatarNotFound
	}
	return nil
}

func (r *AvatarRepository) SoftDelete(ctx context.Context, id uuid.UUID, userID string) (*domain.Avatar, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const sel = `
SELECT id, user_id, file_name, mime_type, size_bytes, width, height, s3_key, thumbnail_s3_keys,
       upload_status, processing_status, created_at, updated_at, deleted_at
FROM avatars
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE`
	a, err := scanAvatar(tx.QueryRow(ctx, sel, id))
	if err != nil {
		return nil, err
	}
	if a.UserID != userID {
		return nil, domain.ErrForbidden
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE avatars SET deleted_at = $2, updated_at = $2 WHERE id = $1`, id, now); err != nil {
		return nil, fmt.Errorf("soft delete: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	a.DeletedAt = &now
	return a, nil
}

func (r *AvatarRepository) IsEventProcessed(ctx context.Context, eventID uuid.UUID) (bool, error) {
	const q = `SELECT 1 FROM processed_events WHERE event_id = $1`
	var n int
	err := r.pool.QueryRow(ctx, q, eventID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check event: %w", err)
	}
	return true, nil
}

func (r *AvatarRepository) MarkEventProcessed(ctx context.Context, eventID uuid.UUID, eventType string) error {
	const q = `INSERT INTO processed_events (event_id, event_type) VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`
	_, err := r.pool.Exec(ctx, q, eventID, eventType)
	if err != nil {
		return fmt.Errorf("mark event: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAvatar(s scanner) (*domain.Avatar, error) {
	var (
		a          domain.Avatar
		thumbsJSON []byte
	)
	err := s.Scan(
		&a.ID, &a.UserID, &a.FileName, &a.MimeType, &a.SizeBytes,
		&a.Width, &a.Height, &a.S3Key, &thumbsJSON,
		&a.UploadStatus, &a.ProcessingStatus,
		&a.CreatedAt, &a.UpdatedAt, &a.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrAvatarNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if len(thumbsJSON) > 0 {
		if err := json.Unmarshal(thumbsJSON, &a.ThumbnailKeys); err != nil {
			return nil, fmt.Errorf("unmarshal thumbnails: %w", err)
		}
	}
	return &a, nil
}
