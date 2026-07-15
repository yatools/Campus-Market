package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
)

type Store struct {
	client *minio.Client
	cfg    config.Config
}

func New(cfg config.Config) (*Store, error) {
	if cfg.S3Endpoint == "" {
		return nil, nil
	}
	parsed, err := url.Parse(cfg.S3Endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("解析 S3_ENDPOINT")
	}
	client, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(cfg.S3AccessKeyID, cfg.S3SecretAccessKey, ""), Secure: parsed.Scheme == "https", Region: cfg.S3Region, BucketLookup: minio.BucketLookupPath})
	if err != nil {
		return nil, err
	}
	return &Store{client: client, cfg: cfg}, nil
}

func (s *Store) EnsureBuckets(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("对象存储未配置")
	}
	for _, bucket := range []string{s.cfg.S3PublicBucket, s.cfg.S3PrivateBucket, s.cfg.S3BackupBucket} {
		exists, err := s.client.BucketExists(ctx, bucket)
		if err != nil {
			return fmt.Errorf("检查 bucket %s: %w", bucket, err)
		}
		if !exists {
			if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s.cfg.S3Region}); err != nil {
				return fmt.Errorf("创建 bucket %s: %w", bucket, err)
			}
		}
	}
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, s.cfg.S3PublicBucket)
	if err := s.client.SetBucketPolicy(ctx, s.cfg.S3PublicBucket, policy); err != nil {
		return fmt.Errorf("设置 public bucket 策略: %w", err)
	}
	return nil
}

func (s *Store) Probe(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("对象存储未配置")
	}
	for _, bucket := range []string{s.cfg.S3PublicBucket, s.cfg.S3PrivateBucket, s.cfg.S3BackupBucket} {
		exists, err := s.client.BucketExists(ctx, bucket)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("bucket %s 不存在", bucket)
		}
	}
	return nil
}

func (s *Store) bucket(scope string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("对象存储未配置")
	}
	switch scope {
	case "public":
		return s.cfg.S3PublicBucket, nil
	case "market_dispute":
		return s.cfg.S3PrivateBucket, nil
	case "backup":
		return s.cfg.S3BackupBucket, nil
	default:
		return "", fmt.Errorf("未知对象范围 %s", scope)
	}
}

func (s *Store) Put(ctx context.Context, scope, key, contentType, cacheControl string, reader io.Reader, size int64) error {
	bucket, err := s.bucket(scope)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, bucket, key, reader, size, minio.PutObjectOptions{ContentType: contentType, CacheControl: cacheControl})
	return err
}

func (s *Store) Remove(ctx context.Context, scope, key string) error {
	bucket, err := s.bucket(scope)
	if err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

func (s *Store) Exists(ctx context.Context, scope, key string) error {
	bucket, err := s.bucket(scope)
	if err != nil {
		return err
	}
	_, err = s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	return err
}

func (s *Store) PresignedGet(ctx context.Context, scope, key string, ttl time.Duration, responseDisposition string) (*url.URL, error) {
	bucket, err := s.bucket(scope)
	if err != nil {
		return nil, err
	}
	params := make(url.Values)
	if responseDisposition != "" {
		params.Set("response-content-disposition", responseDisposition)
	}
	return s.client.PresignedGetObject(ctx, bucket, key, ttl, params)
}

func (s *Store) PublicURL(key string) string {
	if s == nil {
		return ""
	}
	return strings.TrimRight(s.cfg.S3PublicBaseURL, "/") + "/" + strings.TrimLeft(key, "/")
}

func (s *Store) BucketName(scope string) string {
	if s == nil {
		return ""
	}
	bucket, _ := s.bucket(scope)
	return bucket
}
