// Package objectstore implements the Core object storage boundary.
package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/Zubrium/cdn/core/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
)

type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	PathStyle                                      bool
	AllowInsecureHTTP                              bool
}
type S3 struct {
	client *s3.Client
	bucket string
}

// MaxPutObjectBytes follows the S3 single-PUT ceiling in decimal bytes.
const MaxPutObjectBytes int64 = 5_000_000_000

type countReader struct {
	reader io.Reader
	count  int64
}

func (r *countReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.count += int64(n)
	return n, err
}

func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || cfg.Region == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("invalid S3 configuration")
	}
	if endpoint.Scheme == "http" && (!cfg.AllowInsecureHTTP || !loopbackEndpoint(endpoint.Hostname())) {
		return nil, errors.New("unencrypted S3 endpoints must use loopback")
	}
	provider := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")
	clientConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region), awsconfig.WithCredentialsProvider(aws.NewCredentialsCache(provider)))
	if err != nil {
		return nil, fmt.Errorf("S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(clientConfig, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.PathStyle
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if !storage.ValidUUID(key) || body == nil || size < 0 || size > MaxPutObjectBytes || !storage.ValidContentType(contentType) {
		return errors.New("invalid object")
	}
	counted := &countReader{reader: io.LimitReader(body, size)}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: counted, ContentLength: aws.Int64(size), ContentType: aws.String(contentType)}, func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
			return v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware(stack)
		})
	})
	if err != nil {
		return fmt.Errorf("store object: %w", err)
	}
	if counted.count != size {
		return errors.New("object body shorter than declared")
	}
	var extra [1]byte
	n, readErr := body.Read(extra[:])
	if n > 0 {
		return errors.New("object body longer than declared")
	}
	if readErr != io.EOF {
		if readErr != nil {
			return fmt.Errorf("check object body length: %w", readErr)
		}
		return errors.New("object body did not terminate")
	}
	return nil
}

func (s *S3) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	if !storage.ValidUUID(key) {
		return nil, errors.New("invalid object key")
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return out.Body, nil
}

func (s *S3) ReadRange(ctx context.Context, key string, start, end int64) (io.ReadCloser, error) {
	if !storage.ValidUUID(key) || start < 0 || end < start {
		return nil, errors.New("invalid object range")
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Range: aws.String(fmt.Sprintf("bytes=%d-%d", start, end))})
	if err != nil {
		return nil, fmt.Errorf("read object range: %w", err)
	}
	return out.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if !storage.ValidUUID(key) {
		return errors.New("invalid object key")
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func loopbackEndpoint(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RequirePrivate rejects an anonymously readable bucket. Core controls public
// access itself; a public bucket would bypass its authorization checks.
func (s *S3) RequirePrivate(ctx context.Context) error {
	block, err := s.client.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("check bucket public access block: %w", err)
	}
	if block.PublicAccessBlockConfiguration == nil ||
		!aws.ToBool(block.PublicAccessBlockConfiguration.BlockPublicAcls) ||
		!aws.ToBool(block.PublicAccessBlockConfiguration.IgnorePublicAcls) ||
		!aws.ToBool(block.PublicAccessBlockConfiguration.BlockPublicPolicy) ||
		!aws.ToBool(block.PublicAccessBlockConfiguration.RestrictPublicBuckets) {
		return errors.New("all bucket public access block settings must be enabled")
	}
	versioning, err := s.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("check bucket versioning: %w", err)
	}
	if versioning.Status != "" {
		return errors.New("bucket versioning must be disabled for permanent file deletion")
	}
	acl, err := s.client.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("check bucket ACL: %w", err)
	}
	for _, grant := range acl.Grants {
		if grant.Grantee != nil && grant.Grantee.URI != nil && (strings.HasSuffix(*grant.Grantee.URI, "/AllUsers") || strings.HasSuffix(*grant.Grantee.URI, "/AuthenticatedUsers")) {
			return errors.New("bucket ACL permits public access")
		}
	}
	policy, err := s.client.GetBucketPolicyStatus(ctx, &s3.GetBucketPolicyStatusInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchBucketPolicy" || apiErr.ErrorCode() == "NoSuchPolicy") {
			return nil
		}
		return fmt.Errorf("check bucket policy: %w", err)
	}
	if policy.PolicyStatus == nil || policy.PolicyStatus.IsPublic == nil || aws.ToBool(policy.PolicyStatus.IsPublic) {
		return errors.New("bucket policy is public or cannot be verified")
	}
	return nil
}
