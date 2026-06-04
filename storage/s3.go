package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3 fetches objects from S3 (or an S3-compatible endpoint) with the AWS SDK v2.
// Credentials come from the SDK's default chain (IAM role, environment, shared
// config), so deployment-specific auth needs no code changes.
type S3 struct {
	client *s3.Client
}

// S3Options configures the S3 client. EndpointURL is set only for S3-compatible
// backends; when present, path-style addressing is used.
type S3Options struct {
	Region      string
	EndpointURL string
}

func NewS3(ctx context.Context, o S3Options) (*S3, error) {
	loaders := []func(*awsconfig.LoadOptions) error{}
	if o.Region != "" {
		loaders = append(loaders, awsconfig.WithRegion(o.Region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, loaders...)
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}

	var s3opts []func(*s3.Options)
	if o.EndpointURL != "" {
		s3opts = append(s3opts, func(po *s3.Options) {
			po.BaseEndpoint = aws.String(o.EndpointURL)
			po.UsePathStyle = true
		})
	}

	return &S3{client: s3.NewFromConfig(cfg, s3opts...)}, nil
}

func (s *S3) Fetch(ctx context.Context, bucket, key string, w io.Writer) (int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if errors.As(err, &noKey) {
			return 0, ErrNotFound
		}

		return 0, fmt.Errorf("s3 GetObject %s/%s: %w", bucket, key, err)
	}

	defer func() { _ = out.Body.Close() }()

	n, copyErr := io.Copy(w, out.Body)
	if copyErr != nil {
		return n, fmt.Errorf("streaming s3 object: %w", copyErr)
	}

	return n, nil
}
