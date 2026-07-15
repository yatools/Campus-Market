package storage

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
)

func TestStoreScopeAndPublicURL(t *testing.T) {
	cfg := config.Config{S3Endpoint: "http://127.0.0.1:9000", S3Region: "us-east-1", S3AccessKeyID: "access", S3SecretAccessKey: "secret", S3PublicBucket: "public", S3PrivateBucket: "private", S3BackupBucket: "backup", S3PublicBaseURL: "https://media.example.test/base/"}
	store, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for scope, want := range map[string]string{"public": "public", "market_dispute": "private", "backup": "backup"} {
		got, err := store.bucket(scope)
		if err != nil || got != want {
			t.Fatalf("scope %s: %s %v", scope, got, err)
		}
	}
	if _, err := store.bucket("unknown"); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if got := store.PublicURL("2026/image.webp"); got != "https://media.example.test/base/2026/image.webp" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestStoreRejectsInvalidEndpoint(t *testing.T) {
	if _, err := New(config.Config{S3Endpoint: "://bad"}); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}

func TestMinIOPutPresignAndRemove(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT is not configured")
	}
	cfg := config.Config{S3Endpoint: endpoint, S3Region: "us-east-1", S3AccessKeyID: os.Getenv("S3_ACCESS_KEY_ID"), S3SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"), S3PublicBucket: "wutong-storage-test-public", S3PrivateBucket: "wutong-storage-test-private", S3BackupBucket: "wutong-storage-test-backup", S3PublicBaseURL: endpoint + "/wutong-storage-test-public"}
	store, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.EnsureBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	payload := []byte("private evidence")
	if err := store.Put(ctx, "market_dispute", "tests/evidence.txt", "text/plain", "private, no-store", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if err := store.Exists(ctx, "market_dispute", "tests/evidence.txt"); err != nil {
		t.Fatal(err)
	}
	signed, err := store.PresignedGet(ctx, "market_dispute", "tests/evidence.txt", time.Minute, "attachment")
	if err != nil || signed.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("presign failed: %v %v", signed, err)
	}
	if err := store.Remove(ctx, "market_dispute", "tests/evidence.txt"); err != nil {
		t.Fatal(err)
	}
}
