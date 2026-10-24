package blob

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Settings struct {
	Endpoint       string
	PublicEndpoint string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	UsePathStyle   bool
}

type S3Store struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
}

func NewS3Store(settings S3Settings) *S3Store {
	staticCredentials := aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: settings.AccessKey, SecretAccessKey: settings.SecretKey}, nil
	})
	newClient := func(endpoint string) *s3.Client {
		return s3.New(s3.Options{
			Region:       settings.Region,
			BaseEndpoint: aws.String(endpoint),
			UsePathStyle: settings.UsePathStyle,
			Credentials:  staticCredentials,
		})
	}

	publicEndpoint := settings.PublicEndpoint
	if publicEndpoint == "" {
		publicEndpoint = settings.Endpoint
	}
	return &S3Store{
		client:    newClient(settings.Endpoint),
		presigner: s3.NewPresignClient(newClient(publicEndpoint)),
		bucket:    settings.Bucket,
	}
}

func (store *S3Store) SignedUploadURL(ctx context.Context, objectKey string, sizeBytes int64) (string, error) {
	request, err := store.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(store.bucket),
		Key:           aws.String(objectKey),
		ContentLength: aws.Int64(sizeBytes),
	}, s3.WithPresignExpires(SignedURLLifetime))
	if err != nil {
		return "", fmt.Errorf("sign upload url: %w", err)
	}
	return request.URL, nil
}

func (store *S3Store) SignedDownloadURL(ctx context.Context, objectKey string) (string, error) {
	request, err := store.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(SignedURLLifetime))
	if err != nil {
		return "", fmt.Errorf("sign download url: %w", err)
	}
	return request.URL, nil
}

func (store *S3Store) ObjectSize(ctx context.Context, objectKey string) (int64, error) {
	head, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(objectKey),
	})
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return 0, ErrObjectNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read object size: %w", err)
	}
	return aws.ToInt64(head.ContentLength), nil
}
