package domain

import "github.com/google/uuid"

type ProcessingOp struct {
	Type   string `json:"type"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Label  string `json:"label,omitempty"`
}

type AvatarUploadEvent struct {
	EventID  uuid.UUID `json:"event_id"`
	AvatarID uuid.UUID `json:"avatar_id"`
	UserID   string    `json:"user_id"`
	S3Key    string    `json:"s3_key"`
}

type AvatarProcessEvent struct {
	EventID    uuid.UUID      `json:"event_id"`
	AvatarID   uuid.UUID      `json:"avatar_id"`
	Operations []ProcessingOp `json:"operations"`
}

type AvatarDeleteEvent struct {
	EventID  uuid.UUID `json:"event_id"`
	AvatarID uuid.UUID `json:"avatar_id"`
	S3Keys   []string  `json:"s3_keys"`
}
