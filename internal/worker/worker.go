package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/gophprofile/avatars-service/internal/broker/rabbitmq"
	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/domain"
	"github.com/gophprofile/avatars-service/internal/imageproc"
	"github.com/gophprofile/avatars-service/internal/services"
)

type AvatarRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Avatar, error)
	UpdateProcessingResult(ctx context.Context, id uuid.UUID, thumbs domain.ThumbnailKeys, status domain.ProcessingStatus, width, height int) error
	UpdateProcessingStatus(ctx context.Context, id uuid.UUID, status domain.ProcessingStatus) error
	IsEventProcessed(ctx context.Context, eventID uuid.UUID) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID uuid.UUID, eventType string) error
}

type ObjectStorage interface {
	Upload(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	Download(ctx context.Context, key string) (io.ReadCloser, domain.ObjectInfo, error)
	DeleteMany(ctx context.Context, keys []string) error
}

type ThumbnailSpec struct {
	Label  string
	Width  int
	Height int
}

type Worker struct {
	client        *rabbitmq.Client
	repo          AvatarRepository
	storage       ObjectStorage
	cfg           config.BrokerConfig
	thumbnails    []ThumbnailSpec
	retryAttempts int
	retryDelay    time.Duration
}

func New(client *rabbitmq.Client, repo AvatarRepository, storage ObjectStorage, cfg config.BrokerConfig) *Worker {
	return &Worker{
		client:        client,
		repo:          repo,
		storage:       storage,
		cfg:           cfg,
		retryAttempts: cfg.RetryAttempts,
		retryDelay:    cfg.RetryBaseDelay,
		thumbnails: []ThumbnailSpec{
			{Label: "100x100", Width: 100, Height: 100},
			{Label: "300x300", Width: 300, Height: 300},
		},
	}
}

func (w *Worker) Run(ctx context.Context) error {
	uploads, err := w.client.Consume(w.cfg.UploadQueue)
	if err != nil {
		return fmt.Errorf("consume upload queue: %w", err)
	}
	deletes, err := w.client.Consume(w.cfg.DeleteQueue)
	if err != nil {
		return fmt.Errorf("consume delete queue: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		w.dispatchLoop(ctx, uploads, w.handleUpload, "upload")
	}()
	go func() {
		defer wg.Done()
		w.dispatchLoop(ctx, deletes, w.handleDelete, "delete")
	}()
	wg.Wait()
	return nil
}

func (w *Worker) dispatchLoop(ctx context.Context, deliveries <-chan amqp.Delivery, handler func(context.Context, amqp.Delivery) error, kind string) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				log.Printf("worker: %s queue channel closed", kind)
				return
			}
			if err := w.handleWithRetry(ctx, d, handler); err != nil {
				log.Printf("worker: %s handler failed permanently: %v", kind, err)
				_ = d.Nack(false, false)
				continue
			}
			_ = d.Ack(false)
		}
	}
}

func (w *Worker) handleWithRetry(ctx context.Context, d amqp.Delivery, handler func(context.Context, amqp.Delivery) error) error {
	var lastErr error
	for attempt := 0; attempt <= w.retryAttempts; attempt++ {
		err := handler(ctx, d)
		if err == nil {
			return nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		wait := backoff(w.retryDelay, attempt)
		log.Printf("worker: handler attempt %d failed: %v (retry in %s)", attempt+1, err, wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

func (w *Worker) handleUpload(ctx context.Context, d amqp.Delivery) error {
	var event domain.AvatarUploadEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if processed, err := w.repo.IsEventProcessed(ctx, event.EventID); err != nil {
		return err
	} else if processed {
		return nil
	}
	avatar, err := w.repo.GetByID(ctx, event.AvatarID)
	if err != nil {
		if errors.Is(err, domain.ErrAvatarNotFound) {
			return w.repo.MarkEventProcessed(ctx, event.EventID, "upload")
		}
		return err
	}
	if avatar.ProcessingStatus == domain.ProcessingStatusCompleted {
		return w.repo.MarkEventProcessed(ctx, event.EventID, "upload")
	}
	if err := w.repo.UpdateProcessingStatus(ctx, avatar.ID, domain.ProcessingStatusInProgress); err != nil {
		return err
	}
	body, _, err := w.storage.Download(ctx, event.S3Key)
	if err != nil {
		_ = w.repo.UpdateProcessingStatus(ctx, avatar.ID, domain.ProcessingStatusFailed)
		return fmt.Errorf("download original: %w", err)
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		_ = w.repo.UpdateProcessingStatus(ctx, avatar.ID, domain.ProcessingStatusFailed)
		return fmt.Errorf("read original: %w", err)
	}
	decoded, err := imageproc.Decode(bytes.NewReader(raw))
	if err != nil {
		_ = w.repo.UpdateProcessingStatus(ctx, avatar.ID, domain.ProcessingStatusFailed)
		return fmt.Errorf("decode: %w", err)
	}
	thumbs := domain.ThumbnailKeys{}
	for _, spec := range w.thumbnails {
		resized := imageproc.Resize(decoded.Image, spec.Width, spec.Height)
		buf, err := imageproc.EncodeJPEG(resized, 85)
		if err != nil {
			_ = w.repo.UpdateProcessingStatus(ctx, avatar.ID, domain.ProcessingStatusFailed)
			return fmt.Errorf("encode thumbnail %s: %w", spec.Label, err)
		}
		key := services.ThumbnailKey(avatar.ID, spec.Label)
		if err := w.storage.Upload(ctx, key, bytes.NewReader(buf), int64(len(buf)), "image/jpeg"); err != nil {
			_ = w.repo.UpdateProcessingStatus(ctx, avatar.ID, domain.ProcessingStatusFailed)
			return fmt.Errorf("upload thumbnail %s: %w", spec.Label, err)
		}
		thumbs[spec.Label] = key
	}
	if err := w.repo.UpdateProcessingResult(ctx, avatar.ID, thumbs, domain.ProcessingStatusCompleted, decoded.Width, decoded.Height); err != nil {
		return err
	}
	return w.repo.MarkEventProcessed(ctx, event.EventID, "upload")
}

func (w *Worker) handleDelete(ctx context.Context, d amqp.Delivery) error {
	var event domain.AvatarDeleteEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if processed, err := w.repo.IsEventProcessed(ctx, event.EventID); err != nil {
		return err
	} else if processed {
		return nil
	}
	if err := w.storage.DeleteMany(ctx, event.S3Keys); err != nil {
		return fmt.Errorf("delete keys: %w", err)
	}
	return w.repo.MarkEventProcessed(ctx, event.EventID, "delete")
}

func backoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
