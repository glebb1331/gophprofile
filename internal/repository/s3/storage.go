package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/gophprofile/avatars-service/internal/domain"
)

type Storage struct {
	client *minio.Client
	bucket string
}

func New(ctx context.Context, endpoint, accessKey, secretKey, region, bucket string, useSSL bool) (*Storage, error) {
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("init minio client: %w", err)
	}
	s := &Storage{client: cli, bucket: bucket}
	if err := s.ensureBucket(ctx, region); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Storage) ensureBucket(ctx context.Context, region string) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region}); err != nil {
			return fmt.Errorf("create bucket: %w", err)
		}
	}
	return nil
}

func (s *Storage) Upload(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}
	return nil
}

func (s *Storage) Download(ctx context.Context, key string) (io.ReadCloser, domain.ObjectInfo, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, domain.ObjectInfo{}, fmt.Errorf("get object %q: %w", key, err)
	}
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if isNotFoundErr(err) {
			return nil, domain.ObjectInfo{}, domain.ErrAvatarNotFound
		}
		return nil, domain.ObjectInfo{}, fmt.Errorf("stat object %q: %w", key, err)
	}
	return obj, domain.ObjectInfo{
		ContentType: stat.ContentType,
		Size:        stat.Size,
		ETag:        stat.ETag,
	}, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object %q: %w", key, err)
	}
	return nil
}

func (s *Storage) DeleteMany(ctx context.Context, keys []string) error {
	for _, k := range keys {
		if k == "" {
			continue
		}
		if err := s.Delete(ctx, k); err != nil && !errors.Is(err, domain.ErrAvatarNotFound) {
			return err
		}
	}
	return nil
}

func (s *Storage) Ping(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}

func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return true
	}
	return strings.Contains(err.Error(), "key does not exist")
}
