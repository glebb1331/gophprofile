package services

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/domain"
)

type fakeRepo struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]*domain.Avatar
	byUser   map[string][]*domain.Avatar
	createOK bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:     map[uuid.UUID]*domain.Avatar{},
		byUser:   map[string][]*domain.Avatar{},
		createOK: true,
	}
}

func (r *fakeRepo) Create(_ context.Context, a *domain.Avatar) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.createOK {
		return errors.New("create failed")
	}
	r.byID[a.ID] = a
	r.byUser[a.UserID] = append(r.byUser[a.UserID], a)
	return nil
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAvatarNotFound
	}
	return a, nil
}

func (r *fakeRepo) GetLatestByUserID(_ context.Context, userID string) (*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.byUser[userID]
	if len(list) == 0 {
		return nil, domain.ErrAvatarNotFound
	}
	return list[len(list)-1], nil
}

func (r *fakeRepo) ListByUserID(_ context.Context, userID string) ([]*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.Avatar, len(r.byUser[userID]))
	copy(out, r.byUser[userID])
	return out, nil
}

func (r *fakeRepo) SoftDelete(_ context.Context, id uuid.UUID, userID string) (*domain.Avatar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAvatarNotFound
	}
	if a.UserID != userID {
		return nil, domain.ErrForbidden
	}
	delete(r.byID, id)
	return a, nil
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
		return errors.New("upload fail")
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

type fakePublisher struct {
	uploads []domain.AvatarUploadEvent
	deletes []domain.AvatarDeleteEvent
}

func (p *fakePublisher) PublishUpload(_ context.Context, event domain.AvatarUploadEvent) error {
	p.uploads = append(p.uploads, event)
	return nil
}

func (p *fakePublisher) PublishDelete(_ context.Context, event domain.AvatarDeleteEvent) error {
	p.deletes = append(p.deletes, event)
	return nil
}

func smallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func defaultLimits() config.LimitsConfig {
	return config.LimitsConfig{
		MaxUploadBytes: 1 << 20,
		AllowedMime:    []string{"image/jpeg", "image/png", "image/webp"},
	}
}

func TestUploadSuccess(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	pub := &fakePublisher{}
	svc := NewAvatarService(repo, storage, pub, defaultLimits())

	data := smallPNG(t)
	a, err := svc.Upload(context.Background(), UploadInput{
		UserID:   "user-1",
		FileName: "avatar.png",
		Size:     int64(len(data)),
		Reader:   bytes.NewReader(data),
	})
	require.NoError(t, err)
	require.Equal(t, "user-1", a.UserID)
	require.Equal(t, "image/png", a.MimeType)
	require.Equal(t, domain.UploadStatusUploaded, a.UploadStatus)
	require.Equal(t, domain.ProcessingStatusPending, a.ProcessingStatus)
	require.Len(t, pub.uploads, 1)
	require.Equal(t, a.ID, pub.uploads[0].AvatarID)
}

func TestUploadMissingUser(t *testing.T) {
	svc := NewAvatarService(newFakeRepo(), newFakeStorage(), &fakePublisher{}, defaultLimits())
	_, err := svc.Upload(context.Background(), UploadInput{Reader: bytes.NewReader([]byte("x"))})
	require.ErrorIs(t, err, domain.ErrMissingUserID)
}

func TestUploadTooLargeBySizeHint(t *testing.T) {
	svc := NewAvatarService(newFakeRepo(), newFakeStorage(), &fakePublisher{}, config.LimitsConfig{
		MaxUploadBytes: 10,
		AllowedMime:    []string{"image/png"},
	})
	_, err := svc.Upload(context.Background(), UploadInput{
		UserID: "u",
		Size:   500,
		Reader: bytes.NewReader([]byte("hello")),
	})
	require.ErrorIs(t, err, domain.ErrFileTooLarge)
}

func TestUploadInvalidFormat(t *testing.T) {
	svc := NewAvatarService(newFakeRepo(), newFakeStorage(), &fakePublisher{}, defaultLimits())
	_, err := svc.Upload(context.Background(), UploadInput{
		UserID:   "u",
		FileName: "doc.txt",
		Size:     5,
		Reader:   bytes.NewReader([]byte("hello")),
	})
	require.ErrorIs(t, err, domain.ErrInvalidFileFormat)
}

func TestDeleteForbidden(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	svc := NewAvatarService(repo, storage, &fakePublisher{}, defaultLimits())
	data := smallPNG(t)
	a, err := svc.Upload(context.Background(), UploadInput{UserID: "owner", FileName: "a.png", Size: int64(len(data)), Reader: bytes.NewReader(data)})
	require.NoError(t, err)
	err = svc.Delete(context.Background(), a.ID, "other")
	require.ErrorIs(t, err, domain.ErrForbidden)
}

func TestDeleteSuccessPublishesEvent(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	pub := &fakePublisher{}
	svc := NewAvatarService(repo, storage, pub, defaultLimits())
	data := smallPNG(t)
	a, err := svc.Upload(context.Background(), UploadInput{UserID: "u", FileName: "a.png", Size: int64(len(data)), Reader: bytes.NewReader(data)})
	require.NoError(t, err)
	require.NoError(t, svc.Delete(context.Background(), a.ID, "u"))
	require.Len(t, pub.deletes, 1)
	require.Contains(t, pub.deletes[0].S3Keys, a.S3Key)
}

func TestDownloadOriginalAndThumbnail(t *testing.T) {
	repo := newFakeRepo()
	storage := newFakeStorage()
	svc := NewAvatarService(repo, storage, &fakePublisher{}, defaultLimits())
	data := smallPNG(t)
	a, err := svc.Upload(context.Background(), UploadInput{UserID: "u", FileName: "a.png", Size: int64(len(data)), Reader: bytes.NewReader(data)})
	require.NoError(t, err)

	dl, err := svc.DownloadOriginal(context.Background(), a.ID, "")
	require.NoError(t, err)
	got, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	require.Equal(t, data, got)

	_, err = svc.DownloadOriginal(context.Background(), a.ID, "100x100")
	require.ErrorIs(t, err, domain.ErrInvalidThumbnailKey)
}

func TestThumbnailKeyFormat(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	require.Equal(t, "thumbnails/11111111-2222-3333-4444-555555555555/100x100.jpg", ThumbnailKey(id, "100x100"))
}
