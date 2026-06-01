package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorsHaveMessages(t *testing.T) {
	require.Equal(t, "avatar not found", ErrAvatarNotFound.Error())
	require.Equal(t, "forbidden", ErrForbidden.Error())
	require.Equal(t, "invalid file format", ErrInvalidFileFormat.Error())
	require.Equal(t, "file too large", ErrFileTooLarge.Error())
}

func TestUploadStatusValues(t *testing.T) {
	require.Equal(t, UploadStatus("uploading"), UploadStatusUploading)
	require.Equal(t, UploadStatus("uploaded"), UploadStatusUploaded)
}

func TestProcessingStatusValues(t *testing.T) {
	require.Equal(t, ProcessingStatus("pending"), ProcessingStatusPending)
	require.Equal(t, ProcessingStatus("completed"), ProcessingStatusCompleted)
}
