package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type UploadStatus string

const (
	UploadStatusUploading UploadStatus = "uploading"
	UploadStatusUploaded  UploadStatus = "uploaded"
	UploadStatusFailed    UploadStatus = "failed"
)

type ProcessingStatus string

const (
	ProcessingStatusPending    ProcessingStatus = "pending"
	ProcessingStatusInProgress ProcessingStatus = "in_progress"
	ProcessingStatusCompleted  ProcessingStatus = "completed"
	ProcessingStatusFailed     ProcessingStatus = "failed"
)

type ThumbnailKeys map[string]string

type Dimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ObjectInfo struct {
	ContentType string
	Size        int64
	ETag        string
}

type Avatar struct {
	ID               uuid.UUID        `json:"id"`
	UserID           string           `json:"user_id"`
	FileName         string           `json:"file_name"`
	MimeType         string           `json:"mime_type"`
	SizeBytes        int64            `json:"size"`
	S3Key            string           `json:"s3_key"`
	ThumbnailKeys    ThumbnailKeys    `json:"thumbnail_s3_keys,omitempty"`
	Width            int              `json:"-"`
	Height           int              `json:"-"`
	UploadStatus     UploadStatus     `json:"upload_status"`
	ProcessingStatus ProcessingStatus `json:"processing_status"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	DeletedAt        *time.Time       `json:"deleted_at,omitempty"`
}

var (
	ErrAvatarNotFound      = errors.New("avatar not found")
	ErrForbidden           = errors.New("forbidden")
	ErrInvalidFileFormat   = errors.New("invalid file format")
	ErrFileTooLarge        = errors.New("file too large")
	ErrMissingUserID       = errors.New("missing user id")
	ErrMissingFile         = errors.New("missing file")
	ErrInvalidThumbnailKey = errors.New("invalid thumbnail key")
)
