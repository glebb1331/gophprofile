package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gophprofile/avatars-service/internal/broker/rabbitmq"
	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/handlers"
	"github.com/gophprofile/avatars-service/internal/repository/postgres"
	s3repo "github.com/gophprofile/avatars-service/internal/repository/s3"
	"github.com/gophprofile/avatars-service/internal/services"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := postgres.RunMigrations(cfg.Postgres.DSN, cfg.Postgres.MigrationsPath); err != nil {
		log.Fatalf("migrations: %v", err)
	}

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
	publisher := rabbitmq.NewPublisher(broker, cfg.Broker)

	repo := postgres.NewAvatarRepository(pool)
	svc := services.NewAvatarService(repo, storage, publisher, cfg.Limits)
	health := handlers.NewHealth(pool, broker, storage)
	api := handlers.NewAPI(svc, health, cfg.Limits)

	staticDir := os.Getenv("STATIC_DIR")
	if staticDir == "" {
		staticDir = "web/static"
	}
	router := handlers.NewRouter(api, staticDir)

	server := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      router,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("HTTP server listening on %s", cfg.HTTP.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-rootCtx.Done():
		log.Println("shutdown signal received")
	case err := <-serverErr:
		log.Printf("server error: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
}
