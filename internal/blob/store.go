package blob

import (
	"context"
	"errors"
	"time"
)

const SignedURLLifetime = 15 * time.Minute

var ErrObjectNotFound = errors.New("object not found")

type Store interface {
	SignedUploadURL(ctx context.Context, objectKey string, sizeBytes int64) (string, error)
	SignedDownloadURL(ctx context.Context, objectKey string) (string, error)
	ObjectSize(ctx context.Context, objectKey string) (int64, error)
}
