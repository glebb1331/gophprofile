package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTP     HTTPConfig
	Postgres PostgresConfig
	S3       S3Config
	Broker   BrokerConfig
	Limits   LimitsConfig
}

type HTTPConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type PostgresConfig struct {
	DSN            string
	MaxConns       int32
	MigrationsPath string
	ConnectTimeout time.Duration
}

type S3Config struct {
	Endpoint        string
	AccessKey       string
	SecretKey       string
	Bucket          string
	UseSSL          bool
	Region          string
	PublicURLPrefix string
}

type BrokerConfig struct {
	URL            string
	Exchange       string
	UploadQueue    string
	DeleteQueue    string
	UploadRouting  string
	DeleteRouting  string
	ConsumerTag    string
	PrefetchCount  int
	RetryAttempts  int
	RetryBaseDelay time.Duration
}

type LimitsConfig struct {
	MaxUploadBytes int64
	AllowedMime    []string
}

func Load() (*Config, error) {
	cfg := &Config{
		HTTP: HTTPConfig{
			Addr:            getEnv("HTTP_ADDR", ":8080"),
			ReadTimeout:     getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    getEnvDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			ShutdownTimeout: getEnvDuration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Postgres: PostgresConfig{
			DSN:            getEnv("POSTGRES_DSN", "postgres://avatars:avatars@localhost:5432/avatars?sslmode=disable"),
			MaxConns:       int32(getEnvInt("POSTGRES_MAX_CONNS", 10)),
			MigrationsPath: getEnv("POSTGRES_MIGRATIONS_PATH", "file://migrations"),
			ConnectTimeout: getEnvDuration("POSTGRES_CONNECT_TIMEOUT", 10*time.Second),
		},
		S3: S3Config{
			Endpoint:        getEnv("S3_ENDPOINT", "localhost:9000"),
			AccessKey:       getEnv("S3_ACCESS_KEY", "minioadmin"),
			SecretKey:       getEnv("S3_SECRET_KEY", "minioadmin"),
			Bucket:          getEnv("S3_BUCKET", "avatars"),
			UseSSL:          getEnvBool("S3_USE_SSL", false),
			Region:          getEnv("S3_REGION", "us-east-1"),
			PublicURLPrefix: getEnv("S3_PUBLIC_URL_PREFIX", ""),
		},
		Broker: BrokerConfig{
			URL:            getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			Exchange:       getEnv("RABBITMQ_EXCHANGE", "avatars.exchange"),
			UploadQueue:    getEnv("RABBITMQ_UPLOAD_QUEUE", "avatars.upload"),
			DeleteQueue:    getEnv("RABBITMQ_DELETE_QUEUE", "avatars.delete"),
			UploadRouting:  getEnv("RABBITMQ_UPLOAD_ROUTING_KEY", "avatar.uploaded"),
			DeleteRouting:  getEnv("RABBITMQ_DELETE_ROUTING_KEY", "avatar.deleted"),
			ConsumerTag:    getEnv("RABBITMQ_CONSUMER_TAG", "avatar-worker"),
			PrefetchCount:  getEnvInt("RABBITMQ_PREFETCH_COUNT", 8),
			RetryAttempts:  getEnvInt("RABBITMQ_RETRY_ATTEMPTS", 5),
			RetryBaseDelay: getEnvDuration("RABBITMQ_RETRY_BASE_DELAY", time.Second),
		},
		Limits: LimitsConfig{
			MaxUploadBytes: int64(getEnvInt("AVATAR_MAX_UPLOAD_BYTES", 10*1024*1024)),
			AllowedMime: []string{
				"image/jpeg",
				"image/png",
				"image/webp",
			},
		},
	}
	if cfg.S3.Bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET must be set")
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
