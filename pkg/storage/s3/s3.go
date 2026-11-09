package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"websiteengine/pkg/config"
	"websiteengine/pkg/storage"
)

type Store struct {
	client        *s3.Client
	bucket        string
	publicBaseURL string
}

func New(ctx context.Context, cfg config.S3Config) (*Store, error) {
	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(aws.NewCredentialsCache(creds)),
	)
	if err != nil {
		return nil, fmt.Errorf("load S3 config: %w", err)
	}
	endpoint := cfg.Endpoint
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = cfg.ForcePathStyle
		// File objects are streamed from non-seekable readers. The application
		// verifies their SHA-256 before commit; optional trailing SDK checksums
		// cannot be sent over plain HTTP to S3-compatible stores such as MinIO.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	return &Store{client: client, bucket: cfg.Bucket, publicBaseURL: strings.TrimRight(cfg.PublicBaseURL, "/")}, nil
}

func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64, opts storage.PutOptions) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(opts.ContentType),
		CacheControl:  optional(opts.CacheControl),
	})
	if err != nil {
		return fmt.Errorf("put S3 object: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get S3 object: %w", err)
	}
	return out.Body, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.ErrorCode()), "nosuchkey") {
			return nil
		}
		return fmt.Errorf("delete S3 object: %w", err)
	}
	return nil
}

func (s *Store) PublicURL(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.Contains(key, "\\") {
		return "", errors.New("invalid object key")
	}
	base, err := url.Parse(s.publicBaseURL)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + s.bucket + "/" + key
	return base.String(), nil
}

// EnsureBucket creates the configured bucket when missing and removes public
// bucket policies. Bytes are served through the engine's access checks.
func (s *Store) EnsureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || (apiErr.ErrorCode() != "NotFound" && apiErr.ErrorCode() != "NoSuchBucket") {
			return fmt.Errorf("head S3 bucket: %w", err)
		}
		if _, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)}); err != nil {
			var existsErr smithy.APIError
			if !errors.As(err, &existsErr) || existsErr.ErrorCode() != "BucketAlreadyOwnedByYou" {
				return fmt.Errorf("create S3 bucket: %w", err)
			}
		}
	}
	if _, err := s.client.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{
		Bucket: aws.String(s.bucket),
	}); err != nil {
		return fmt.Errorf("remove S3 public policy: %w", err)
	}
	return nil
}

func (s *Store) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("stat S3 object: %w", err)
	}
	return storage.ObjectInfo{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType)}, nil
}

func (s *Store) GetRange(ctx context.Context, key string, start, end int64) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Range: aws.String(fmt.Sprintf("bytes=%d-%d", start, end))})
	if err != nil {
		return nil, fmt.Errorf("read S3 range: %w", err)
	}
	return out.Body, nil
}

// RequirePrivate refuses a bucket policy granting anonymous object reads.
// Existing deployments must remove that policy before private files are used.
func (s *Store) RequirePrivate(ctx context.Context) error {
	acl, err := s.client.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("check private bucket ACL: %w", err)
	}
	for _, grant := range acl.Grants {
		if grant.Grantee != nil && grant.Grantee.URI != nil && (strings.HasSuffix(*grant.Grantee.URI, "/AllUsers") || strings.HasSuffix(*grant.Grantee.URI, "/AuthenticatedUsers")) {
			return errors.New("S3 bucket ACL must not grant public access")
		}
	}
	policy, err := s.client.GetBucketPolicyStatus(ctx, &s3.GetBucketPolicyStatusInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchBucketPolicy" || apiErr.ErrorCode() == "NoSuchPolicy") {
			return nil
		}
		return fmt.Errorf("check private bucket policy: %w", err)
	}
	if policy.PolicyStatus != nil && aws.ToBool(policy.PolicyStatus.IsPublic) {
		return errors.New("S3 bucket must be private; public policy would bypass file authorization")
	}
	return nil
}

func (s *Store) Ready(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("S3 bucket is not ready: %w", err)
	}
	return nil
}

func optional(v string) *string {
	if v == "" {
		return nil
	}
	return aws.String(v)
}
