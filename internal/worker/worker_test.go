package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"

	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/domain"
)

type fakeRepo struct {
	mu        sync.Mutex
	avatars   map[uuid.UUID]*domain.Avatar
	processed map[uuid.UUID]string
	updates   int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		avatars:   map[uuid.UUID]*domain.Avatar{},
		processed: map[uuid.UUID]string{},
	}
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.avatars[id]
	if !ok {
		return nil, domain.ErrAvatarNotFound
	}
	return a, nil
}

func (r *fakeRepo) UpdateProcessingResult(_ context.Context, id uuid.UUID, thumbs domain.ThumbnailKeys, status domain.ProcessingStatus, width, height int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.avatars[id]
	if !ok {
		return domain.ErrAvatarNotFound
	}
	a.ThumbnailKeys = thumbs
	a.ProcessingStatus = status
	a.Width = width
	a.Height = height
	r.updates++
	return nil
}

func (r *fakeRepo) UpdateProcessingStatus(_ context.Context, id uuid.UUID, status domain.ProcessingStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.avatars[id]
	if !ok {
		return domain.ErrAvatarNotFound
	}
	a.ProcessingStatus = status
	return nil
}

func (r *fakeRepo) IsEventProcessed(_ context.Context, eventID uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.processed[eventID]
	return ok, nil
}

func (r *fakeRepo) MarkEventProcessed(_ context.Context, eventID uuid.UUID, eventType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.processed[eventID] = eventType
	return nil
}

type fakeStorage struct {
	mu     sync.Mutex
	blobs  map[string][]byte
	mime   map[string]string
	failOn string
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{blobs: map[string][]byte{}, mime: map[string]string{}}
}

func (s *fakeStorage) Upload(_ context.Context, key string, body io.Reader, _ int64, contentType string) error {
	if key == s.failOn {
		return errors.New("upload failure")
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[key] = data
	s.mime[key] = contentType
	return nil
}

func (s *fakeStorage) Download(_ context.Context, key string) (io.ReadCloser, domain.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.blobs[key]
	if !ok {
		return nil, domain.ObjectInfo{}, domain.ErrAvatarNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), domain.ObjectInfo{ContentType: s.mime[key], Size: int64(len(data))}, nil
}

func (s *fakeStorage) DeleteMany(_ context.Context, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		delete(s.blobs, k)
	}
	return nil
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func newTestWorker(repo AvatarRepository, storage ObjectStorage) *Worker {
	return &Worker{
		repo:          repo,
		storage:       storage,
		cfg:           config.BrokerConfig{},
		retryAttempts: 0,
		retryDelay:    time.Millisecond,
		thumbnails: []ThumbnailSpec{
			{Label: "100x100", Width: 100, Height: 100},
			{Label: "300x300", Width: 300, Height: 300},
		},
	}
}

func TestHandleUploadGeneratesThumbnails(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	w := newTestWorker(repo, storage)

	avatarID := uuid.New()
	repo.avatars[avatarID] = &domain.Avatar{
		ID:               avatarID,
		UserID:           "u",
		S3Key:            "originals/u/a.png",
		ProcessingStatus: domain.ProcessingStatusPending,
	}
	pngBytes := makePNG(t, 200, 200)
	storage.blobs["originals/u/a.png"] = pngBytes
	storage.mime["originals/u/a.png"] = "image/png"

	event := domain.AvatarUploadEvent{
		EventID:  uuid.New(),
		AvatarID: avatarID,
		UserID:   "u",
		S3Key:    "originals/u/a.png",
	}
	body, _ := json.Marshal(event)
	err := w.handleUpload(context.Background(), amqp.Delivery{Body: body})
	require.NoError(t, err)
	require.Equal(t, domain.ProcessingStatusCompleted, repo.avatars[avatarID].ProcessingStatus)
	require.Equal(t, 200, repo.avatars[avatarID].Width)
	require.Len(t, repo.avatars[avatarID].ThumbnailKeys, 2)
}

func TestHandleUploadIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	w := newTestWorker(repo, storage)

	avatarID := uuid.New()
	repo.avatars[avatarID] = &domain.Avatar{
		ID:               avatarID,
		UserID:           "u",
		S3Key:            "originals/u/a.png",
		ProcessingStatus: domain.ProcessingStatusPending,
	}
	storage.blobs["originals/u/a.png"] = makePNG(t, 64, 64)
	storage.mime["originals/u/a.png"] = "image/png"

	event := domain.AvatarUploadEvent{
		EventID:  uuid.New(),
		AvatarID: avatarID,
		UserID:   "u",
		S3Key:    "originals/u/a.png",
	}
	body, _ := json.Marshal(event)
	require.NoError(t, w.handleUpload(context.Background(), amqp.Delivery{Body: body}))
	firstUpdates := repo.updates
	require.NoError(t, w.handleUpload(context.Background(), amqp.Delivery{Body: body}))
	require.Equal(t, firstUpdates, repo.updates, "second invocation must be a no-op")
}

func TestHandleUploadMissingAvatarMarksProcessed(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	w := newTestWorker(repo, storage)

	event := domain.AvatarUploadEvent{
		EventID:  uuid.New(),
		AvatarID: uuid.New(),
		UserID:   "u",
		S3Key:    "missing",
	}
	body, _ := json.Marshal(event)
	require.NoError(t, w.handleUpload(context.Background(), amqp.Delivery{Body: body}))
	_, ok := repo.processed[event.EventID]
	require.True(t, ok)
}

func TestHandleDeleteRemovesObjects(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	storage.blobs["k1"] = []byte("a")
	storage.blobs["k2"] = []byte("b")
	w := newTestWorker(repo, storage)

	event := domain.AvatarDeleteEvent{
		EventID:  uuid.New(),
		AvatarID: uuid.New(),
		S3Keys:   []string{"k1", "k2"},
	}
	body, _ := json.Marshal(event)
	require.NoError(t, w.handleDelete(context.Background(), amqp.Delivery{Body: body}))
	require.Empty(t, storage.blobs)
	_, ok := repo.processed[event.EventID]
	require.True(t, ok)
}

func TestHandleWithRetrySucceedsAfterTransientError(t *testing.T) {
	w := newTestWorker(newFakeRepo(), newFakeStorage())
	w.retryAttempts = 3
	w.retryDelay = time.Microsecond

	calls := 0
	handler := func(_ context.Context, _ amqp.Delivery) error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}
		return nil
	}
	err := w.handleWithRetry(context.Background(), amqp.Delivery{}, handler)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestHandleWithRetryGivesUp(t *testing.T) {
	w := newTestWorker(newFakeRepo(), newFakeStorage())
	w.retryAttempts = 2
	w.retryDelay = time.Microsecond

	handler := func(_ context.Context, _ amqp.Delivery) error {
		return errors.New("persistent")
	}
	err := w.handleWithRetry(context.Background(), amqp.Delivery{}, handler)
	require.Error(t, err)
}

func TestHandleWithRetryHonoursCancellation(t *testing.T) {
	w := newTestWorker(newFakeRepo(), newFakeStorage())
	w.retryAttempts = 5
	w.retryDelay = time.Second

	ctx, cancel := context.WithCancel(context.Background())
	handler := func(_ context.Context, _ amqp.Delivery) error {
		cancel()
		return errors.New("temp")
	}
	err := w.handleWithRetry(ctx, amqp.Delivery{}, handler)
	require.ErrorIs(t, err, context.Canceled)
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	require.Equal(t, time.Second, backoff(time.Second, 0))
	require.Equal(t, 2*time.Second, backoff(time.Second, 1))
	require.Equal(t, 30*time.Second, backoff(time.Second, 20))
}
