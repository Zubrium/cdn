package s3

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"net/url"
	"strings"
	"time"
	"websiteengine/pkg/storage"
)

// Compose copies verified parts server-side. The final key is invisible until
// CompleteMultipartUpload. Failed uploads are aborted; process-crash leftovers
// are reclaimed by CleanupMultipart using the configured session TTL.
func (s *Store) Compose(ctx context.Context, key string, parts []string, opts storage.PutOptions) error {
	started, err := s.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ContentType: aws.String(opts.ContentType), CacheControl: optional(opts.CacheControl)})
	if err != nil {
		return fmt.Errorf("start S3 composition: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_, _ = s.client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: started.UploadId})
		}
	}()
	list := make([]types.CompletedPart, 0, len(parts))
	for i, part := range parts {
		source := url.PathEscape(s.bucket + "/" + part)
		copied, err := s.client.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: started.UploadId, PartNumber: aws.Int32(int32(i + 1)), CopySource: aws.String(source)})
		if err != nil {
			return fmt.Errorf("copy S3 part: %w", err)
		}
		if copied.CopyPartResult == nil || copied.CopyPartResult.ETag == nil {
			return fmt.Errorf("S3 part copy omitted etag")
		}
		list = append(list, types.CompletedPart{ETag: copied.CopyPartResult.ETag, PartNumber: aws.Int32(int32(i + 1))})
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{Bucket: aws.String(s.bucket), Key: aws.String(key), UploadId: started.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: list}})
	if err != nil {
		return fmt.Errorf("complete S3 composition: %w", err)
	}
	completed = true
	return nil
}
func (s *Store) CleanupMultipart(ctx context.Context, before time.Time) error {
	pager := awss3.NewListMultipartUploadsPaginator(s.client, &awss3.ListMultipartUploadsInput{Bucket: aws.String(s.bucket), Prefix: aws.String("media/")})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list incomplete S3 uploads: %w", err)
		}
		for _, upload := range page.Uploads {
			if upload.Initiated == nil || !upload.Initiated.Before(before) || upload.Key == nil || !strings.HasPrefix(*upload.Key, "media/") {
				continue
			}
			if _, err = s.client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{Bucket: aws.String(s.bucket), Key: upload.Key, UploadId: upload.UploadId}); err != nil {
				return fmt.Errorf("abort incomplete S3 upload: %w", err)
			}
		}
	}
	return nil
}
