package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/domain"
	"github.com/gophprofile/avatars-service/internal/imageproc"
)

type AvatarRepository interface {
	Create(ctx context.Context, a *domain.Avatar) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Avatar, error)
	GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error)
	ListByUserID(ctx context.Context, userID string) ([]*domain.Avatar, error)
	SoftDelete(ctx context.Context, id uuid.UUID, userID string) (*domain.Avatar, error)
}

type ObjectStorage interface {
	Upload(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	Download(ctx context.Context, key string) (io.ReadCloser, domain.ObjectInfo, error)
}

type EventPublisher interface {
	PublishUpload(ctx context.Context, event domain.AvatarUploadEvent) error
	PublishDelete(ctx context.Context, event domain.AvatarDeleteEvent) error
}

type AvatarService struct {
	repo      AvatarRepository
	storage   ObjectStorage
	publisher EventPublisher
	limits    config.LimitsConfig
}

func NewAvatarService(repo AvatarRepository, storage ObjectStorage, publisher EventPublisher, limits config.LimitsConfig) *AvatarService {
	return &AvatarService{repo: repo, storage: storage, publisher: publisher, limits: limits}
}

type UploadInput struct {
	UserID   string
	FileName string
	Size     int64
	Reader   io.Reader
	Header   *multipart.FileHeader
}

func (s *AvatarService) Upload(ctx context.Context, in UploadInput) (*domain.Avatar, error) {
	if in.UserID == "" {
		return nil, domain.ErrMissingUserID
	}
	if in.Reader == nil {
		return nil, domain.ErrMissingFile
	}
	if s.limits.MaxUploadBytes > 0 && in.Size > s.limits.MaxUploadBytes {
		return nil, domain.ErrFileTooLarge
	}

	limit := s.limits.MaxUploadBytes
	if limit <= 0 {
		limit = 10 * 1024 * 1024
	}
	data, err := io.ReadAll(io.LimitReader(in.Reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, domain.ErrFileTooLarge
	}

	mime := imageproc.DetectMime(headSnippet(data))
	if !imageproc.IsAllowedMime(mime, s.limits.AllowedMime) {
		return nil, domain.ErrInvalidFileFormat
	}

	avatarID := uuid.New()
	ext := normalizeExt(in.FileName, mime)
	s3Key := buildOriginalKey(in.UserID, avatarID, ext)

	if err := s.storage.Upload(ctx, s3Key, bytes.NewReader(data), int64(len(data)), mime); err != nil {
		return nil, fmt.Errorf("upload to storage: %w", err)
	}

	avatar := &domain.Avatar{
		ID:               avatarID,
		UserID:           in.UserID,
		FileName:         filepath.Base(in.FileName),
		MimeType:         mime,
		SizeBytes:        int64(len(data)),
		S3Key:            s3Key,
		ThumbnailKeys:    domain.ThumbnailKeys{},
		UploadStatus:     domain.UploadStatusUploaded,
		ProcessingStatus: domain.ProcessingStatusPending,
	}
	if err := s.repo.Create(ctx, avatar); err != nil {
		return nil, fmt.Errorf("save avatar: %w", err)
	}

	if s.publisher != nil {
		event := domain.AvatarUploadEvent{
			EventID:  uuid.New(),
			AvatarID: avatar.ID,
			UserID:   avatar.UserID,
			S3Key:    avatar.S3Key,
		}
		if err := s.publisher.PublishUpload(ctx, event); err != nil {
			return nil, fmt.Errorf("publish event: %w", err)
		}
	}
	return avatar, nil
}

type DownloadResult struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
	ETag        string
}

func (s *AvatarService) DownloadOriginal(ctx context.Context, id uuid.UUID, size string) (*DownloadResult, error) {
	avatar, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	key := avatar.S3Key
	if size != "" && size != "original" {
		if k, ok := avatar.ThumbnailKeys[size]; ok && k != "" {
			key = k
		} else {
			return nil, domain.ErrInvalidThumbnailKey
		}
	}
	body, info, err := s.storage.Download(ctx, key)
	if err != nil {
		return nil, err
	}
	return &DownloadResult{
		Body:        body,
		ContentType: info.ContentType,
		Size:        info.Size,
		ETag:        info.ETag,
	}, nil
}

func (s *AvatarService) DownloadForUser(ctx context.Context, userID, size string) (*DownloadResult, *domain.Avatar, error) {
	avatar, err := s.repo.GetLatestByUserID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	dl, err := s.DownloadOriginal(ctx, avatar.ID, size)
	if err != nil {
		return nil, nil, err
	}
	return dl, avatar, nil
}

func (s *AvatarService) Get(ctx context.Context, id uuid.UUID) (*domain.Avatar, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *AvatarService) ListByUser(ctx context.Context, userID string) ([]*domain.Avatar, error) {
	return s.repo.ListByUserID(ctx, userID)
}

func (s *AvatarService) Delete(ctx context.Context, id uuid.UUID, userID string) error {
	avatar, err := s.repo.SoftDelete(ctx, id, userID)
	if err != nil {
		return err
	}
	if s.publisher == nil {
		return nil
	}
	keys := []string{avatar.S3Key}
	for _, v := range avatar.ThumbnailKeys {
		if v != "" {
			keys = append(keys, v)
		}
	}
	event := domain.AvatarDeleteEvent{
		EventID:  uuid.New(),
		AvatarID: avatar.ID,
		S3Keys:   keys,
	}
	if err := s.publisher.PublishDelete(ctx, event); err != nil {
		return fmt.Errorf("publish delete: %w", err)
	}
	return nil
}

func (s *AvatarService) DeleteLatestForUser(ctx context.Context, userID, actorID string) error {
	avatar, err := s.repo.GetLatestByUserID(ctx, userID)
	if err != nil {
		return err
	}
	return s.Delete(ctx, avatar.ID, actorID)
}

func headSnippet(data []byte) []byte {
	if len(data) > 512 {
		return data[:512]
	}
	return data
}

func normalizeExt(fileName, mime string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	switch mime {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	}
	if ext == "" {
		return "bin"
	}
	return ext
}

func buildOriginalKey(userID string, id uuid.UUID, ext string) string {
	return fmt.Sprintf("originals/%s/%s.%s", userID, id.String(), ext)
}

func ThumbnailKey(id uuid.UUID, label string) string {
	return fmt.Sprintf("thumbnails/%s/%s.jpg", id.String(), label)
}
