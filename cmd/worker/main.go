package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gophprofile/avatars-service/internal/broker/rabbitmq"
	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/repository/postgres"
	s3repo "github.com/gophprofile/avatars-service/internal/repository/s3"
	"github.com/gophprofile/avatars-service/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := postgres.NewPool(rootCtx, cfg.Postgres.DSN, cfg.Postgres.MaxConns, cfg.Postgres.ConnectTimeout)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()

	storage, err := s3repo.New(rootCtx, cfg.S3.Endpoint, cfg.S3.AccessKey, cfg.S3.SecretKey, cfg.S3.Region, cfg.S3.Bucket, cfg.S3.UseSSL)
	if err != nil {
		log.Fatalf("s3: %v", err)
	}

	broker, err := rabbitmq.New(rootCtx, cfg.Broker)
	if err != nil {
		log.Fatalf("rabbitmq: %v", err)
	}
	defer broker.Close()

	repo := postgres.NewAvatarRepository(pool)
	w := worker.New(broker, repo, storage, cfg.Broker)
	log.Println("worker started")
	if err := w.Run(rootCtx); err != nil {
		log.Fatalf("worker run: %v", err)
	}
	log.Println("worker stopped")
}
