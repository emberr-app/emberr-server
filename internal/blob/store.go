package blob

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

const SignedURLLifetime = 15 * time.Minute

var ErrObjectNotFound = errors.New("object not found")

type Store interface {
	SignedUploadURL(ctx context.Context, objectKey string, sizeBytes int64) (string, error)
	SignedDownloadURL(ctx context.Context, objectKey string) (string, error)
	ObjectSize(ctx context.Context, objectKey string) (int64, error)
	ObjectSizesUnder(ctx context.Context, prefix string) (map[string]int64, error)
	DeleteObject(ctx context.Context, objectKey string) error
}

func WithoutRequestURL(err error) error {
	var requestError *url.Error
	if errors.As(err, &requestError) {
		return fmt.Errorf("%s request failed: %w", requestError.Op, requestError.Err)
	}
	return err
}
