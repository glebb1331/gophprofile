package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/domain"
	"github.com/gophprofile/avatars-service/internal/services"
)

type AvatarService interface {
	Upload(ctx context.Context, in services.UploadInput) (*domain.Avatar, error)
	Get(ctx context.Context, id uuid.UUID) (*domain.Avatar, error)
	ListByUser(ctx context.Context, userID string) ([]*domain.Avatar, error)
	DownloadOriginal(ctx context.Context, id uuid.UUID, size string) (*services.DownloadResult, error)
	DownloadForUser(ctx context.Context, userID, size string) (*services.DownloadResult, *domain.Avatar, error)
	Delete(ctx context.Context, id uuid.UUID, userID string) error
	DeleteLatestForUser(ctx context.Context, userID, actorID string) error
}

type HealthChecker interface {
	Check(ctx context.Context) HealthReport
}

type API struct {
	service AvatarService
	health  HealthChecker
	limits  config.LimitsConfig
}

func NewAPI(service AvatarService, health HealthChecker, limits config.LimitsConfig) *API {
	return &API{service: service, health: health, limits: limits}
}

type avatarResponse struct {
	ID        uuid.UUID `json:"id"`
	UserID    string    `json:"user_id"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type avatarMetadataResponse struct {
	ID               uuid.UUID               `json:"id"`
	UserID           string                  `json:"user_id"`
	FileName         string                  `json:"file_name"`
	MimeType         string                  `json:"mime_type"`
	Size             int64                   `json:"size"`
	Dimensions       domain.Dimensions       `json:"dimensions"`
	Thumbnails       []thumbnailLink         `json:"thumbnails"`
	UploadStatus     domain.UploadStatus     `json:"upload_status"`
	ProcessingStatus domain.ProcessingStatus `json:"processing_status"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type thumbnailLink struct {
	Size string `json:"size"`
	URL  string `json:"url"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	MaxSize int64  `json:"max_size,omitempty"`
}

func (a *API) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "X-User-ID header is required", "")
		return
	}
	if err := r.ParseMultipartForm(a.limits.MaxUploadBytes + 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid multipart form", err.Error())
		return
	}
	file, header, err := pickUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing file", err.Error())
		return
	}
	defer file.Close()

	if a.limits.MaxUploadBytes > 0 && header.Size > a.limits.MaxUploadBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
			Error:   "File too large",
			MaxSize: a.limits.MaxUploadBytes,
		})
		return
	}

	avatar, err := a.service.Upload(r.Context(), services.UploadInput{
		UserID:   userID,
		FileName: header.Filename,
		Size:     header.Size,
		Reader:   file,
		Header:   header,
	})
	if err != nil {
		a.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, avatarResponse{
		ID:        avatar.ID,
		UserID:    avatar.UserID,
		URL:       fmt.Sprintf("/api/v1/avatars/%s", avatar.ID),
		Status:    string(avatar.ProcessingStatus),
		CreatedAt: avatar.CreatedAt,
	})
}

func (a *API) GetAvatar(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid avatar id", err.Error())
		return
	}
	size := r.URL.Query().Get("size")
	dl, err := a.service.DownloadOriginal(r.Context(), id, size)
	if err != nil {
		a.writeServiceError(w, err)
		return
	}
	streamObject(w, r, dl)
}

func (a *API) GetUserAvatar(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required", "")
		return
	}
	size := r.URL.Query().Get("size")
	dl, _, err := a.service.DownloadForUser(r.Context(), userID, size)
	if err != nil {
		a.writeServiceError(w, err)
		return
	}
	streamObject(w, r, dl)
}

func (a *API) GetAvatarMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid avatar id", err.Error())
		return
	}
	avatar, err := a.service.Get(r.Context(), id)
	if err != nil {
		a.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMetadata(avatar))
}

func (a *API) ListUserAvatars(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required", "")
		return
	}
	avatars, err := a.service.ListByUser(r.Context(), userID)
	if err != nil {
		a.writeServiceError(w, err)
		return
	}
	out := make([]avatarMetadataResponse, 0, len(avatars))
	for _, av := range avatars {
		out = append(out, toMetadata(av))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

func (a *API) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid avatar id", err.Error())
		return
	}
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "X-User-ID header is required", "")
		return
	}
	if err := a.service.Delete(r.Context(), id, userID); err != nil {
		a.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) DeleteUserAvatar(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required", "")
		return
	}
	actorID := r.Header.Get("X-User-ID")
	if actorID == "" {
		writeError(w, http.StatusBadRequest, "X-User-ID header is required", "")
		return
	}
	if err := a.service.DeleteLatestForUser(r.Context(), userID, actorID); err != nil {
		a.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type HealthReport struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	report := a.health.Check(r.Context())
	code := http.StatusOK
	if report.Status != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, report)
}

func (a *API) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrAvatarNotFound):
		writeError(w, http.StatusNotFound, "Avatar not found", "")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "Forbidden", "You can only modify your own avatars")
	case errors.Is(err, domain.ErrInvalidFileFormat):
		writeError(w, http.StatusBadRequest, "Invalid file format", "Supported formats: jpeg, png, webp")
	case errors.Is(err, domain.ErrFileTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
			Error:   "File too large",
			MaxSize: a.limits.MaxUploadBytes,
		})
	case errors.Is(err, domain.ErrMissingUserID):
		writeError(w, http.StatusBadRequest, "Missing user id", "")
	case errors.Is(err, domain.ErrMissingFile):
		writeError(w, http.StatusBadRequest, "Missing file", "")
	case errors.Is(err, domain.ErrInvalidThumbnailKey):
		writeError(w, http.StatusBadRequest, "Requested size is not available", "")
	default:
		writeError(w, http.StatusInternalServerError, "Internal server error", err.Error())
	}
}

func pickUpload(r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	for _, name := range []string{"file", "image", "avatar"} {
		f, h, err := r.FormFile(name)
		if err == nil {
			return f, h, nil
		}
	}
	return nil, nil, errors.New("file field is required (file/image/avatar)")
}

func streamObject(w http.ResponseWriter, r *http.Request, dl *services.DownloadResult) {
	defer dl.Body.Close()
	w.Header().Set("Content-Type", dl.ContentType)
	if dl.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(dl.Size, 10))
	}
	if dl.ETag != "" {
		w.Header().Set("ETag", dl.ETag)
		if match := r.Header.Get("If-None-Match"); match != "" && match == dl.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, dl.Body)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg, details string) {
	writeJSON(w, status, errorResponse{Error: msg, Details: details})
}

func toMetadata(a *domain.Avatar) avatarMetadataResponse {
	thumbs := make([]thumbnailLink, 0, len(a.ThumbnailKeys))
	for size := range a.ThumbnailKeys {
		thumbs = append(thumbs, thumbnailLink{
			Size: size,
			URL:  fmt.Sprintf("/api/v1/avatars/%s?size=%s", a.ID, size),
		})
	}
	return avatarMetadataResponse{
		ID:               a.ID,
		UserID:           a.UserID,
		FileName:         a.FileName,
		MimeType:         a.MimeType,
		Size:             a.SizeBytes,
		Dimensions:       domain.Dimensions{Width: a.Width, Height: a.Height},
		Thumbnails:       thumbs,
		UploadStatus:     a.UploadStatus,
		ProcessingStatus: a.ProcessingStatus,
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
	}
}
