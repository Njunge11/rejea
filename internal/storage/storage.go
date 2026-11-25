// Package storage keeps the files of the service in an S3 bucket.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	transfertypes "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go/logging"
	smithyhttp "github.com/aws/smithy-go/transport/http"
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
	// Endpoint is the address of a file store that is not AWS S3 but has
	// its API, such as the store of compose.yaml. With "", the bucket is
	// in AWS S3.
	Endpoint string
	// PublicEndpoint is the address at which a client of the service
	// reaches that store: an upload URL starts with it. With "", it is
	// Endpoint. The two are different when the service is in a container:
	// the service reaches the store by its name in the container network,
	// and a browser reaches it through a port of the machine.
	PublicEndpoint string
	// UploadURLLife is the time for which an upload URL can start an upload.
	UploadURLLife time.Duration
	// ViewURLLife is the time for which a view URL can read a file.
	ViewURLLife time.Duration
	// CloudFront is the distribution in front of the bucket. A view URL
	// goes to it when it is set.
	CloudFront CloudFront
	// MaxFileBytes is the largest file that a client can upload.
	MaxFileBytes int64
	// ReadTimeout is the longest time that a connection to S3 can be silent
	// before a read fails. Each byte that arrives starts the time again,
	// so a slow download that moves does not fail.
	ReadTimeout time.Duration
	// RangeBytes is the size of one piece when the service reads a file in
	// pieces.
	RangeBytes int64
	// CheckTimeout is the longest time that New waits for the answer of
	// the store.
	CheckTimeout time.Duration
}

// The reasons for which New fails because of the settings or the state of
// the store. The caller tells the developer what to do for each one.
var (
	// ErrNotSet: the bucket or its region is not set.
	ErrNotSet = errors.New("the file store has no bucket or no region")
	// ErrEndpoint: an endpoint is not an http or https URL with a host.
	ErrEndpoint = errors.New("the address of the file store is not valid")
	// ErrNoCredentials: the AWS SDK found no credentials.
	ErrNoCredentials = errors.New("the file store has no credentials")
	// ErrNoAnswer: no store answers at the address.
	ErrNoAnswer = errors.New("no file store answers")
	// ErrRefused: the store refuses the credentials for the bucket.
	ErrRefused = errors.New("the file store refuses the credentials")
	// ErrNoBucket: the store has no bucket with this name.
	ErrNoBucket = errors.New("the file store has no bucket with this name")
	// ErrRegion: the bucket is in a different AWS Region.
	ErrRegion = errors.New("the bucket is in a different region")
)

// Store reads and writes the files in the bucket.
type Store struct {
	bucket        string
	uploadURLLife time.Duration
	viewURLLife   time.Duration
	// viewer signs the view URLs of CloudFront. It is nil when the view
	// URLs go to the bucket itself.
	viewer     *viewer
	rangeBytes int64
	client     *s3.Client
	presign    *s3.PresignClient
	transfer   *transfermanager.Client
}

// Fault is an error of the file storage or of the network. The content of
// the file is not its cause, so a new attempt can succeed.
type Fault struct{ err error }

func (f *Fault) Error() string { return f.err.Error() }

// Unwrap gives the error that the AWS SDK returned.
func (f *Fault) Unwrap() error { return f.err }

// IsFault tells if an error is a Fault or has a Fault as its cause.
func IsFault(err error) bool {
	_, ok := errors.AsType[*Fault](err)
	return ok
}

// New makes a Store and confirms that the bucket answers. The AWS
// credentials come from the default sources of the AWS SDK: the
// environment, a profile, or the role of the machine. The SDK logs through
// logger.
func New(ctx context.Context, logger *slog.Logger, cfg Config) (*Store, error) {
	if cfg.Bucket == "" || cfg.Region == "" {
		return nil, ErrNotSet
	}
	for _, endpoint := range []string{cfg.Endpoint, cfg.PublicEndpoint} {
		if endpoint == "" {
			continue
		}
		address, err := url.Parse(endpoint)
		if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" {
			return nil, fmt.Errorf("%w: %q must be an http or https URL with a host, such as http://127.0.0.1:8333",
				ErrEndpoint, endpoint)
		}
	}
	if cfg.CheckTimeout <= 0 {
		return nil, fmt.Errorf("the time limit of the check of the file store is %s: it must be more than zero", cfg.CheckTimeout)
	}
	if cfg.UploadURLLife <= 0 || cfg.UploadURLLife > maxUploadURLLife {
		return nil, fmt.Errorf("the life of an upload URL is %s: S3 permits more than zero and at most %s",
			cfg.UploadURLLife, maxUploadURLLife)
	}
	viewer, err := newViewer(cfg.CloudFront)
	if err != nil {
		return nil, err
	}
	// A URL of the bucket has the limit of S3. CloudFront has no limit.
	if cfg.ViewURLLife <= 0 || (viewer == nil && cfg.ViewURLLife > maxUploadURLLife) {
		return nil, fmt.Errorf("the life of a view URL is %s: it must be more than zero, and S3 permits at most %s",
			cfg.ViewURLLife, maxUploadURLLife)
	}
	// The longest life of a URL that the AWS credentials sign.
	signedLife := cfg.UploadURLLife
	if viewer == nil {
		signedLife = max(signedLife, cfg.ViewURLLife)
	}
	if cfg.MaxFileBytes < 1 || cfg.MaxFileBytes > maxPutBytes {
		return nil, fmt.Errorf("the largest file is %d bytes: S3 permits 1 to %d bytes in one upload request",
			cfg.MaxFileBytes, int64(maxPutBytes))
	}
	if cfg.ReadTimeout <= 0 {
		return nil, fmt.Errorf("the S3 read timeout is %s: it must be more than zero", cfg.ReadTimeout)
	}
	if cfg.RangeBytes < 1 {
		return nil, fmt.Errorf("the size of one piece of a file is %d bytes: it must be 1 or more", cfg.RangeBytes)
	}
	// Only a machine in AWS has a role. For a store that is not AWS S3,
	// the SDK does not ask for one: the question takes seconds to fail.
	machineRole := imds.ClientDefaultEnableState
	if cfg.Endpoint != "" {
		machineRole = imds.ClientDisabled
	}
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithEC2IMDSClientEnableState(machineRole),
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
		// S3 refuses a signed URL when the credentials that signed it are
		// expired, even if the URL has time left. Thus the SDK must get new
		// credentials when the current ones have less time left than the
		// life of a signed URL.
		config.WithCredentialsCacheOptions(func(o *aws.CredentialsCacheOptions) {
			o.ExpiryWindow = signedLife
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("load the AWS configuration: %w", err)
	}
	// New credentials must live longer than a signed URL. If not, each
	// URL stops before its time and each request gets new credentials. The
	// SDK gives an expiry time that has the window subtracted already, so
	// a time in the past means that the credentials are too short.
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCredentials, err)
	}
	if left := time.Until(creds.Expires); creds.CanExpire && left <= 0 {
		return nil, fmt.Errorf("the AWS credentials live for %s, but a signed URL lives for %s: use a longer session",
			(left + signedLife).Round(time.Second), signedLife)
	}
	client := s3.NewFromConfig(awsCfg, at(cfg.Endpoint))
	if err := check(ctx, client, cfg); err != nil {
		return nil, err
	}
	if viewer != nil {
		checkCtx, cancel := context.WithTimeout(ctx, cfg.CheckTimeout)
		defer cancel()
		if err := viewer.check(checkCtx); err != nil {
			return nil, err
		}
		logger.InfoContext(ctx, "the view URLs go through CloudFront", "domain", cfg.CloudFront.Domain)
	}
	// The address is a part of the signature of an upload URL, so the URL
	// is signed for the address that its client uses.
	signer := client
	if cfg.PublicEndpoint != "" && cfg.PublicEndpoint != cfg.Endpoint {
		signer = s3.NewFromConfig(awsCfg, at(cfg.PublicEndpoint))
	}
	return &Store{
		bucket:        cfg.Bucket,
		uploadURLLife: cfg.UploadURLLife,
		viewURLLife:   cfg.ViewURLLife,
		viewer:        viewer,
		rangeBytes:    cfg.RangeBytes,
		client:        client,
		presign:       s3.NewPresignClient(signer),
		transfer: transfermanager.New(client, func(o *transfermanager.Options) {
			// The context of a job that ran out of time is cancelled, but
			// the parts of its upload must be removed from S3 all the same.
			o.FailTimeout = cfg.ReadTimeout
		}),
	}, nil
}

// check asks the store for the bucket. A wrong address, wrong credentials
// or a wrong bucket name thus stop the service when it starts, and not at
// the first upload.
func check(ctx context.Context, client *s3.Client, cfg Config) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.CheckTimeout)
	defer cancel()
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)})
	if err == nil {
		return nil
	}
	where := fmt.Sprintf("bucket %q in AWS S3, region %s", cfg.Bucket, cfg.Region)
	if cfg.Endpoint != "" {
		where = fmt.Sprintf("bucket %q at %s", cfg.Bucket, cfg.Endpoint)
	}
	// The store gives no reason in words: the answer to this request has
	// no body. The status code is the reason.
	status := 0
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok {
		status = response.HTTPStatusCode()
	}
	switch status {
	case 0: // No answer came.
		return fmt.Errorf("%w: %s: %w", ErrNoAnswer, where, err)
	case http.StatusForbidden, http.StatusUnauthorized:
		return fmt.Errorf("%w: %s: %w", ErrRefused, where, err)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s: %w", ErrNoBucket, where, err)
	case http.StatusMovedPermanently:
		return fmt.Errorf("%w: %s: %w", ErrRegion, where, err)
	default:
		return fmt.Errorf("the file store answers with status %d for %s: %w", status, where, err)
	}
}

// at makes an S3 client use the file store at the given address. Such a
// store has the bucket in the path of the address, not in its host name:
// a host name for each bucket needs DNS records that a local store does not
// have. With "", the client uses AWS S3.
func at(endpoint string) func(*s3.Options) {
	return func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	}
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
// An error of the stream is a Fault.
func (s *Store) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, &Fault{fmt.Errorf("download %q: %w", key, err)}
	}
	return &download{ReadCloser: object.Body, key: key}, nil
}

// download is the stream of a file. It marks each error as a Fault.
type download struct {
	io.ReadCloser

	key string
}

func (d *download) Read(p []byte) (int, error) {
	n, err := d.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = &Fault{fmt.Errorf("read %q: %w", d.key, err)}
	}
	return n, err
}

// Upload sends a stream to the bucket as the file with the key. The length
// of the stream is not known at the start, so a large stream goes to S3 in
// parts, from memory, with no copy on a disk. An error is a Fault, also
// when the stream itself failed: the caller knows the errors of its stream.
//
// The content type is what S3 and CloudFront tell a browser that reads the
// file. With "", S3 gives the file its default type.
func (s *Store) Upload(ctx context.Context, key, contentType string, content io.Reader) error {
	input := &transfermanager.UploadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   content,
		// One checksum for the full file, also when the file went in parts.
		// The AWS SDK can then check each later download of the file.
		ChecksumType: transfertypes.ChecksumTypeFullObject,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if _, err := s.transfer.UploadObject(ctx, input); err != nil {
		return &Fault{fmt.Errorf("upload %q: %w", key, err)}
	}
	return nil
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

// maxDeleteKeys is the largest number of files that one delete request of
// S3 can name.
const maxDeleteKeys = 1000

// Delete removes the files with the keys from the bucket. It is not an
// error when a file is not there.
func (s *Store) Delete(ctx context.Context, keys []string) error {
	for batch := range slices.Chunk(keys, maxDeleteKeys) {
		objects := make([]types.ObjectIdentifier, len(batch))
		for i, key := range batch {
			objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
		}
		result, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return &Fault{fmt.Errorf("delete %d files: %w", len(batch), err)}
		}
		if len(result.Errors) > 0 {
			first := result.Errors[0]
			return &Fault{fmt.Errorf("delete %d files: S3 refused %d, the first is %q: %s",
				len(batch), len(result.Errors), aws.ToString(first.Key), aws.ToString(first.Message))}
		}
	}
	return nil
}
