package offsite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Bucket is the storage the backups go to.
type Bucket interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// Keys returns every object key.
	Keys(ctx context.Context) ([]string, error)
}

type s3Bucket struct {
	c      *minio.Client
	bucket string
}

// OpenS3 connects to an S3-compatible bucket. endpoint is a host[:port], or a URL whose
// scheme decides between TLS and plain HTTP (https by default).
func OpenS3(cfg Config) (Bucket, error) {
	host, secure := cfg.Endpoint, true
	if strings.Contains(host, "://") {
		u, err := url.Parse(host)
		if err != nil || u.Host == "" {
			return nil, errors.New("endpoint must be a host name or an http(s) URL")
		}
		host, secure = u.Host, u.Scheme == "https"
	}
	c, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	return &s3Bucket{c: c, bucket: cfg.Bucket}, nil
}

func (b *s3Bucket) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	_, err := b.c.PutObject(ctx, b.bucket, key, r, size, minio.PutObjectOptions{})
	return err
}

func (b *s3Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o, err := b.c.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := o.Stat(); err != nil { // GetObject itself does not fail for a missing key
		o.Close()
		return nil, err
	}
	return o, nil
}

func (b *s3Bucket) Delete(ctx context.Context, key string) error {
	return b.c.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{})
}

func (b *s3Bucket) Keys(ctx context.Context) ([]string, error) {
	var keys []string
	for o := range b.c.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		keys = append(keys, o.Key)
	}
	return keys, nil
}
