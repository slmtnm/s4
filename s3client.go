package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client wraps the AWS S3 client with our configuration
type S3Client struct {
	client *s3.Client
	config *S3Config
}

// S3Object represents an S3 object with metadata
type S3Object struct {
	Key          string
	Size         int64
	LastModified string
	IsDir        bool
}

// NewS3Client creates a new S3 client from configuration
func NewS3Client(cfg *S3Config) (*S3Client, error) {
	awsConfig, err := config.LoadDefaultConfig(context.TODO(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey,
			cfg.SecretKey,
			"",
		)),
		config.WithRegion(cfg.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.GetEndpointURL())
		o.UsePathStyle = true // Required for MinIO and some S3-compatible services
	})

	return &S3Client{
		client: client,
		config: cfg,
	}, nil
}

// ListObjects lists objects in a bucket with a prefix
func (c *S3Client) ListObjects(ctx context.Context, bucket, prefix string) ([]S3Object, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	}

	result, err := c.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	var objects []S3Object

	// Add directories (common prefixes)
	for _, prefix := range result.CommonPrefixes {
		key := strings.TrimSuffix(*prefix.Prefix, "/")
		if key != "" {
			objects = append(objects, S3Object{
				Key:   key,
				IsDir: true,
			})
		}
	}

	// Add files
	for _, obj := range result.Contents {
		key := *obj.Key
		if !strings.HasSuffix(key, "/") { // Skip directory markers
			objects = append(objects, S3Object{
				Key:          key,
				Size:         *obj.Size,
				LastModified: obj.LastModified.Format("2006-01-02 15:04:05"),
				IsDir:        false,
			})
		}
	}

	return objects, nil
}

// GetObject downloads an object from S3
func (c *S3Client) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	result, err := c.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}

	return data, nil
}

// PutObject uploads an object to S3
func (c *S3Client) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(string(data)),
	}

	_, err := c.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	return nil
}

// DeleteObject deletes an object from S3
func (c *S3Client) DeleteObject(ctx context.Context, bucket, key string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	_, err := c.client.DeleteObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

// CopyObject copies an object within the same bucket
func (c *S3Client) CopyObject(ctx context.Context, bucket, sourceKey, destKey string) error {
	copySource := fmt.Sprintf("%s/%s", bucket, sourceKey)
	
	input := &s3.CopyObjectInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(destKey),
		CopySource: aws.String(copySource),
	}

	_, err := c.client.CopyObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// RenameObject renames an object by copying it to the new key and deleting the old one
func (c *S3Client) RenameObject(ctx context.Context, bucket, oldKey, newKey string) error {
	// First, copy the object to the new key
	err := c.CopyObject(ctx, bucket, oldKey, newKey)
	if err != nil {
		return fmt.Errorf("failed to copy object during rename: %w", err)
	}

	// Then delete the original object
	err = c.DeleteObject(ctx, bucket, oldKey)
	if err != nil {
		return fmt.Errorf("failed to delete original object during rename: %w", err)
	}

	return nil
}

// HeadBucket checks if a bucket exists and is accessible
func (c *S3Client) HeadBucket(ctx context.Context, bucket string) error {
	input := &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	}

	_, err := c.client.HeadBucket(ctx, input)
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "NoSuchBucket") {
			return fmt.Errorf("bucket '%s' does not exist", bucket)
		}
		return fmt.Errorf("failed to access bucket '%s': %w", bucket, err)
	}

	return nil
}