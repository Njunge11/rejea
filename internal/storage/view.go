package storage

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/cloudfront/sign"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// CloudFront is the settings of the CloudFront distribution in front of the
// bucket. With no values, a view URL goes to the bucket itself.
type CloudFront struct {
	// Domain is the domain name of the distribution, such as
	// d111111abcdef8.cloudfront.net.
	Domain string
	// KeyPairID is the ID of the public key in CloudFront. The key is in a
	// key group that the distribution trusts.
	KeyPairID string
	// PrivateKeyFile is the path of the private key of that public key, as
	// a PEM file.
	PrivateKeyFile string
}

// The reasons for which New fails because of the settings of CloudFront.
var (
	// ErrViewSettings: some of the CloudFront settings have a value, and
	// some have none.
	ErrViewSettings = errors.New("the CloudFront settings are not complete")
	// ErrViewDomain: the domain is not a host name.
	ErrViewDomain = errors.New("the CloudFront domain is not valid")
	// ErrViewKey: the private key file is not there, or it is not a key
	// that CloudFront accepts.
	ErrViewKey = errors.New("the CloudFront private key is not usable")
	// ErrViewNoAnswer: no distribution answers at the domain.
	ErrViewNoAnswer = errors.New("no CloudFront distribution answers")
	// ErrViewKeyPair: the distribution does not know the key pair ID.
	ErrViewKeyPair = errors.New("CloudFront does not know the key pair ID")
	// ErrViewSignature: the distribution refuses a signature of the
	// private key.
	ErrViewSignature = errors.New("CloudFront refuses the signature of the private key")
)

// viewer signs the view URLs of CloudFront.
type viewer struct {
	// base is the address of the distribution, with no path.
	base string
	// keyPairID tells CloudFront which public key proves the signature.
	keyPairID string
	key       crypto.Signer
}

// newViewer reads the CloudFront settings. With no settings, it gives no
// viewer and no error.
func newViewer(cfg CloudFront) (*viewer, error) {
	set := 0
	for _, value := range []string{cfg.Domain, cfg.KeyPairID, cfg.PrivateKeyFile} {
		if strings.TrimSpace(value) != "" {
			set++
		}
	}
	switch set {
	case 0:
		return nil, nil //nolint:nilnil // No viewer and no error: the view URLs go to the bucket.
	case 3:
	default:
		return nil, ErrViewSettings
	}
	// The domain is a host name only: the service makes the https URL.
	address, err := url.Parse("https://" + cfg.Domain)
	if err != nil || address.Host != cfg.Domain || strings.ContainsAny(cfg.Domain, "/ ") {
		return nil, fmt.Errorf("%w: %q must be a host name only, such as d111111abcdef8.cloudfront.net", ErrViewDomain, cfg.Domain)
	}
	key, err := loadViewKey(cfg.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	return &viewer{base: "https://" + cfg.Domain, keyPairID: cfg.KeyPairID, key: key}, nil
}

// checkLife is the life of the URL that proves the settings.
const checkLife = 5 * time.Minute

// errorCode finds the code in an error answer of CloudFront or of S3.
var errorCode = regexp.MustCompile(`<Code>([^<]+)</Code>`)

// check proves the settings with one request: a signed URL for a file that
// is not in the bucket. CloudFront examines the signature before it asks
// the bucket for the file. Measured in November 2026:
//
//   - A key pair ID that CloudFront does not know: status 403 with the code
//     InvalidKey.
//   - A signature of a different private key: status 403 from CloudFront
//     itself (the header "Server: CloudFront").
//   - A correct signature: the answer of the bucket for a file that it does
//     not have (the header "Server: AmazonS3").
//
// An answer that is not one of the first two is no fault.
func (v *viewer) check(ctx context.Context) error {
	const folder = "rejea-start-check/"
	query, err := v.sign(folder, time.Now().Add(checkLife))
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.base+"/"+folder+"none"+query, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		// The error of the HTTP client has the URL with the signature: the
		// cause alone tells what failed.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return fmt.Errorf("%w at %s: %w", ErrViewNoAnswer, v.base, err)
	}
	defer response.Body.Close() //nolint:errcheck // The read of the body found each error.
	// An error answer is a small XML document.
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096)) //nolint:errcheck // An answer with no body has no code.
	code := ""
	if found := errorCode.FindSubmatch(body); found != nil {
		code = string(found[1])
	}
	switch {
	case code == "InvalidKey":
		return fmt.Errorf("%w: %s", ErrViewKeyPair, v.keyPairID)
	case response.StatusCode == http.StatusForbidden && response.Header.Get("Server") == "CloudFront":
		return fmt.Errorf("%w: CloudFront answers %q", ErrViewSignature, code)
	}
	return nil
}

// loadViewKey reads the private key: an RSA key of 2,048 bits in a PEM
// file, in the form PKCS #8 or PKCS #1 ("openssl genrsa" writes one of the
// two). CloudFront accepts an ECDSA key also. This service does not: no
// run proved such a key.
// https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-trusted-signers.html
func loadViewKey(path string) (crypto.Signer, error) {
	content, err := os.ReadFile(path) //nolint:gosec // The path is a setting of the developer.
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrViewKey, err)
	}
	block, _ := pem.Decode(content)
	if block == nil {
		return nil, fmt.Errorf("%w: the file %s is not a PEM file", ErrViewKey, path)
	}
	var key any
	if key, err = x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		if key, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
			return nil, fmt.Errorf("%w: the file %s (%s) is not an RSA private key", ErrViewKey, path, block.Type)
		}
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: the key of %s has the type %T, and the service needs an RSA key", ErrViewKey, path, key)
	}
	if bits := rsaKey.N.BitLen(); bits != 2048 {
		return nil, fmt.Errorf("%w: the RSA key of %s has %d bits, and CloudFront accepts 2,048", ErrViewKey, path, bits)
	}
	return rsaKey, nil
}

// ViewURLs gives a URL for each file, with which a browser reads the file
// with no AWS credentials until the time that ViewURLs returns. Each key is
// in the folder, which ends with "/".
//
// With CloudFront, the URLs have one signature for the folder: a custom
// policy with a wildcard, which the CloudFront documentation gives for
// more than one file. With no CloudFront, each URL is a presigned URL of
// the bucket.
// https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-signed-urls.html
func (s *Store) ViewURLs(ctx context.Context, folder string, keys []string) ([]string, time.Time, error) {
	if !strings.HasSuffix(folder, "/") {
		return nil, time.Time{}, fmt.Errorf("the folder %q does not end with a slash", folder)
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, folder) {
			return nil, time.Time{}, fmt.Errorf("the file %q is not in the folder %q", key, folder)
		}
	}
	// CloudFront and S3 count the life in seconds.
	expires := time.Now().Add(s.viewURLLife).Truncate(time.Second)
	urls := make([]string, len(keys))
	if s.viewer == nil {
		for i, key := range keys {
			request, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(s.bucket),
				Key:    aws.String(key),
			}, s3.WithPresignExpires(s.viewURLLife))
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("sign the view URL of %q: %w", key, err)
			}
			urls[i] = request.URL
		}
		return urls, expires, nil
	}
	query, err := s.viewer.sign(folder, expires)
	if err != nil {
		return nil, time.Time{}, err
	}
	for i, key := range keys {
		urls[i] = s.viewer.base + "/" + escapePath(key) + query
	}
	return urls, expires, nil
}

// sign gives the query string that permits a read of each file of the
// folder until the time. The signature uses SHA-256, which CloudFront
// accepts in place of its default, SHA-1.
func (v *viewer) sign(folder string, expires time.Time) (string, error) {
	policy := &sign.Policy{Statements: []sign.Statement{{
		Resource:  v.base + "/" + escapePath(folder) + "*",
		Condition: sign.Condition{DateLessThan: sign.NewAWSEpochTime(expires)},
	}}}
	signature, encoded, err := policy.SignWithAlgorithm(v.key, sign.HashSHA256)
	if err != nil {
		return "", fmt.Errorf("sign the view URLs of the folder %q: %w", folder, err)
	}
	return "?Policy=" + string(encoded) + "&Signature=" + string(signature) +
		"&Key-Pair-Id=" + url.QueryEscape(v.keyPairID) + "&Hash-Algorithm=" + string(sign.HashSHA256), nil
}

// escapePath gives a storage key as the path of a URL.
func escapePath(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
