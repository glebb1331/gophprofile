package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "")
	t.Setenv("S3_BUCKET", "avatars")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("HTTP_READ_TIMEOUT", "")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTP.Addr)
	require.Equal(t, 15*time.Second, cfg.HTTP.ReadTimeout)
	require.Equal(t, int64(10*1024*1024), cfg.Limits.MaxUploadBytes)
	require.Contains(t, cfg.Limits.AllowedMime, "image/jpeg")
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("HTTP_READ_TIMEOUT", "3s")
	t.Setenv("AVATAR_MAX_UPLOAD_BYTES", "2048")
	t.Setenv("S3_USE_SSL", "true")
	t.Setenv("RABBITMQ_PREFETCH_COUNT", "16")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTP.Addr)
	require.Equal(t, 3*time.Second, cfg.HTTP.ReadTimeout)
	require.Equal(t, int64(2048), cfg.Limits.MaxUploadBytes)
	require.True(t, cfg.S3.UseSSL)
	require.Equal(t, 16, cfg.Broker.PrefetchCount)
}

func TestLoadInvalidDurationsFallBack(t *testing.T) {
	t.Setenv("HTTP_READ_TIMEOUT", "not-a-duration")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, cfg.HTTP.ReadTimeout)
}
