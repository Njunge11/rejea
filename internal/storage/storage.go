// Package storage keeps the files of the service in an S3 bucket.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// maxUploadURLLife is the longest life that S3 gives a presigned URL.
const maxUploadURLLife = 7 * 24 * time.Hour

// Config is the settings of the file storage.
type Config struct {
	// Bucket is the name of the S3 bucket and Region is its AWS Region.
	Bucket string
	Region string
	// UploadURLLife is the time for which an upload URL can start an upload.
	UploadURLLife time.Duration
}

// Store reads and writes the files in the bucket.
type Store struct {
	bucket        string
	uploadURLLife time.Duration
	client        *s3.Client
	presign       *s3.PresignClient
}

// New makes a Store. The AWS credentials come from the default sources of
// the AWS SDK: the environment, a profile, or the role of the machine.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Bucket == "" || cfg.Region == "" {
		return nil, errors.New("the S3 bucket and its region are not set")
	}
	if cfg.UploadURLLife <= 0 || cfg.UploadURLLife > maxUploadURLLife {
		return nil, fmt.Errorf("the life of an upload URL is %s: S3 permits more than zero and at most %s",
			cfg.UploadURLLife, maxUploadURLLife)
	}
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		// S3 refuses an upload URL when the credentials that signed it are
		// expired, even if the URL has time left. Thus the SDK must get new
		// credentials when the current ones have less time left than the
		// life of an upload URL.
		config.WithCredentialsCacheOptions(func(o *aws.CredentialsCacheOptions) {
			o.ExpiryWindow = cfg.UploadURLLife
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("load the AWS configuration: %w", err)
	}
	// New credentials must live longer than an upload URL. If not, each
	// URL stops before its time and each request gets new credentials. The
	// SDK gives an expiry time that has the window subtracted already, so
	// a time in the past means that the credentials are too short.
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("get the AWS credentials: %w", err)
	}
	if left := time.Until(creds.Expires); creds.CanExpire && left <= 0 {
		return nil, fmt.Errorf("the AWS credentials live for %s, but an upload URL lives for %s: use a longer session",
			(left + cfg.UploadURLLife).Round(time.Second), cfg.UploadURLLife)
	}
	client := s3.NewFromConfig(awsCfg)
	return &Store{
		bucket:        cfg.Bucket,
		uploadURLLife: cfg.UploadURLLife,
		client:        client,
		presign:       s3.NewPresignClient(client),
	}, nil
}

// UploadURL gives a URL to which a client sends one file with an HTTP PUT,
// with no AWS credentials. The file gets the given key. The size is part
// of the signature: S3 refuses a body of a different size.
func (s *Store) UploadURL(ctx context.Context, key string, size int64) (string, error) {
	request, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(s.uploadURLLife))
	if err != nil {
		return "", fmt.Errorf("sign the upload URL of %q: %w", key, err)
	}
	return request.URL, nil
}

// Download gives the content of a file as a stream. The caller closes it.
func (s *Store) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("download %q: %w", key, err)
	}
	return object.Body, nil
}
