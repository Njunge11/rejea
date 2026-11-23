// Package storage keeps the files of the service in an S3 bucket.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go/logging"
)

// The limits of S3.
const (
	// maxUploadURLLife is the longest life of a presigned URL.
	maxUploadURLLife = 7 * 24 * time.Hour
	// maxPutBytes is the largest object that one PUT request can send. The
	// S3 documentation says "5 GB".
	maxPutBytes = 5_000_000_000
)

// Config is the settings of the file storage.
type Config struct {
	// Bucket is the name of the S3 bucket and Region is its AWS Region.
	Bucket string
	Region string
	// UploadURLLife is the time for which an upload URL can start an upload.
	UploadURLLife time.Duration
	// MaxFileBytes is the largest file that a client can upload.
	MaxFileBytes int64
	// ReadTimeout is the longest time that a connection to S3 can be silent
	// before a read fails. Each byte that arrives starts the time again,
	// so a slow download that moves does not fail.
	ReadTimeout time.Duration
}

// Store reads and writes the files in the bucket.
type Store struct {
	bucket        string
	uploadURLLife time.Duration
	client        *s3.Client
	presign       *s3.PresignClient
}

// New makes a Store. The AWS credentials come from the default sources of
// the AWS SDK: the environment, a profile, or the role of the machine. The
// SDK logs through logger.
func New(ctx context.Context, logger *slog.Logger, cfg Config) (*Store, error) {
	if cfg.Bucket == "" || cfg.Region == "" {
		return nil, errors.New("the S3 bucket and its region are not set")
	}
	if cfg.UploadURLLife <= 0 || cfg.UploadURLLife > maxUploadURLLife {
		return nil, fmt.Errorf("the life of an upload URL is %s: S3 permits more than zero and at most %s",
			cfg.UploadURLLife, maxUploadURLLife)
	}
	if cfg.MaxFileBytes < 1 || cfg.MaxFileBytes > maxPutBytes {
		return nil, fmt.Errorf("the largest file is %d bytes: S3 permits 1 to %d bytes in one upload request",
			cfg.MaxFileBytes, int64(maxPutBytes))
	}
	if cfg.ReadTimeout <= 0 {
		return nil, fmt.Errorf("the S3 read timeout is %s: it must be more than zero", cfg.ReadTimeout)
	}
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithLogger(logging.LoggerFunc(func(class logging.Classification, format string, v ...any) {
			level := slog.LevelDebug
			if class == logging.Warn {
				level = slog.LevelWarn
			}
			logger.Log(ctx, level, "aws sdk: "+fmt.Sprintf(format, v...))
		})),
		// The SDK has no read timeout for S3. Without one, a download on a
		// connection that goes silent waits until its job is cancelled.
		config.WithHTTPClient(awshttp.NewBuildableClient().WithReadTimeout(cfg.ReadTimeout)),
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

// Exists tells if the bucket has a file with the key. S3 can tell a file
// that is not there from a request that it refuses only when the
// credentials have the s3:ListBucket permission.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	if _, notFound := errors.AsType[*types.NotFound](err); notFound {
		return false, nil
	}
	return false, fmt.Errorf("look for %q: %w", key, err)
}
