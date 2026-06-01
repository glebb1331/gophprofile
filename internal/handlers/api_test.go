package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/domain"
	"github.com/gophprofile/avatars-service/internal/services"
)

type fakeService struct {
	mu        sync.Mutex
	avatars   map[uuid.UUID]*domain.Avatar
	uploaded  *domain.Avatar
	deleted   []uuid.UUID
	uploadErr error
	getErr    error
	deleteErr error
}

func newFakeService() *fakeService {
	return &fakeService{avatars: map[uuid.UUID]*domain.Avatar{}}
}

func (f *fakeService) Upload(_ context.Context, in services.UploadInput) (*domain.Avatar, error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	a := &domain.Avatar{
		ID:               uuid.New(),
		UserID:           in.UserID,
		FileName:         in.FileName,
		MimeType:         "image/png",
		SizeBytes:        in.Size,
		S3Key:            "k",
		UploadStatus:     domain.UploadStatusUploaded,
		ProcessingStatus: domain.ProcessingStatusPending,
		ThumbnailKeys:    domain.ThumbnailKeys{},
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploaded = a
	f.avatars[a.ID] = a
	return a, nil
}

func (f *fakeService) Get(_ context.Context, id uuid.UUID) (*domain.Avatar, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.avatars[id]
	if !ok {
		return nil, domain.ErrAvatarNotFound
	}
	return a, nil
}

func (f *fakeService) ListByUser(_ context.Context, _ string) ([]*domain.Avatar, error) {
	return nil, nil
}

func (f *fakeService) DownloadOriginal(_ context.Context, id uuid.UUID, _ string) (*services.DownloadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.avatars[id]; !ok {
		return nil, domain.ErrAvatarNotFound
	}
	return &services.DownloadResult{
		Body:        io.NopCloser(strings.NewReader("payload")),
		ContentType: "image/png",
		Size:        7,
		ETag:        "abc",
	}, nil
}

func (f *fakeService) DownloadForUser(_ context.Context, _, _ string) (*services.DownloadResult, *domain.Avatar, error) {
	return nil, nil, domain.ErrAvatarNotFound
}

func (f *fakeService) Delete(_ context.Context, id uuid.UUID, _ string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.avatars[id]; !ok {
		return domain.ErrAvatarNotFound
	}
	delete(f.avatars, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeService) DeleteLatestForUser(_ context.Context, _, _ string) error {
	return nil
}

type fakeHealth struct{}

func (fakeHealth) Check(_ context.Context) HealthReport {
	return HealthReport{Status: "ok", Components: map[string]string{"postgres": "ok"}}
}

func smallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func buildUploadRequest(t *testing.T, userID string, fileBytes []byte, fileName string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", fileName)
	require.NoError(t, err)
	_, err = part.Write(fileBytes)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	return req
}

func newTestRouter(t *testing.T, svc AvatarService) http.Handler {
	t.Helper()
	limits := config.LimitsConfig{MaxUploadBytes: 1 << 20, AllowedMime: []string{"image/png"}}
	api := NewAPI(svc, fakeHealth{}, limits)
	return NewRouter(api, "")
}

func TestUploadHandlerSuccess(t *testing.T) {
	svc := newFakeService()
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u-1", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var resp avatarResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "u-1", resp.UserID)
	require.NotEqual(t, uuid.Nil, resp.ID)
}

func TestUploadHandlerMissingUserID(t *testing.T) {
	r := newTestRouter(t, newFakeService())
	req := buildUploadRequest(t, "", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUploadHandlerInvalidFormat(t *testing.T) {
	svc := newFakeService()
	svc.uploadErr = domain.ErrInvalidFileFormat
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u", []byte("not-an-image"), "a.txt")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Invalid file format")
}

func TestUploadHandlerTooLarge(t *testing.T) {
	svc := newFakeService()
	svc.uploadErr = domain.ErrFileTooLarge
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestGetAvatarHandler(t *testing.T) {
	svc := newFakeService()
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	id := svc.uploaded.ID

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String(), nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)
	require.Equal(t, "image/png", getRec.Header().Get("Content-Type"))
	require.Equal(t, "payload", getRec.Body.String())
}

func TestGetAvatarMetadataHandler(t *testing.T) {
	svc := newFakeService()
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	id := svc.uploaded.ID

	metaReq := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"/metadata", nil)
	metaRec := httptest.NewRecorder()
	r.ServeHTTP(metaRec, metaReq)
	require.Equal(t, http.StatusOK, metaRec.Code)
	var meta avatarMetadataResponse
	require.NoError(t, json.Unmarshal(metaRec.Body.Bytes(), &meta))
	require.Equal(t, id, meta.ID)
}

func TestDeleteAvatarHandler(t *testing.T) {
	svc := newFakeService()
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	id := svc.uploaded.ID

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	delReq.Header.Set("X-User-ID", "u")
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)
	require.Equal(t, http.StatusNoContent, delRec.Code)
	require.Len(t, svc.deleted, 1)
}

func TestDeleteAvatarHandlerNotFound(t *testing.T) {
	svc := newFakeService()
	svc.deleteErr = domain.ErrAvatarNotFound
	r := newTestRouter(t, svc)
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+uuid.New().String(), nil)
	delReq.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, delReq)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHealth(t *testing.T) {
	r := newTestRouter(t, newFakeService())
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
}

func TestDeleteForbidden(t *testing.T) {
	svc := newFakeService()
	svc.deleteErr = domain.ErrForbidden
	r := newTestRouter(t, svc)
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+uuid.New().String(), nil)
	delReq.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, delReq)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestListUserAvatarsHandler(t *testing.T) {
	svc := newFakeService()
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/users/u/avatars", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	require.Contains(t, listRec.Body.String(), `"items"`)
}

func TestGetUserAvatarNotFound(t *testing.T) {
	r := newTestRouter(t, newFakeService())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/u/avatar", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetAvatarInvalidUUID(t *testing.T) {
	r := newTestRouter(t, newFakeService())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeleteAvatarMissingUserID(t *testing.T) {
	r := newTestRouter(t, newFakeService())
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeleteUserAvatarHandler(t *testing.T) {
	r := newTestRouter(t, newFakeService())
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/u/avatar", nil)
	req.Header.Set("X-User-ID", "u")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestUploadHandlerInternalError(t *testing.T) {
	svc := newFakeService()
	svc.uploadErr = errors.New("boom")
	r := newTestRouter(t, svc)
	req := buildUploadRequest(t, "u", smallPNG(t), "a.png")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
