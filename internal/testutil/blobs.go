package testutil

import (
	"context"
	"sync"

	"github.com/emberr-app/emberr-server/internal/blob"
)

const fakeBlobAddress = "https://blobs.test/"

type FakeBlobStore struct {
	lock        sync.Mutex
	objectSizes map[string]int64
}

func (store *FakeBlobStore) SignedUploadURL(ctx context.Context, objectKey string, sizeBytes int64) (string, error) {
	return fakeBlobAddress + "upload/" + objectKey, nil
}

func (store *FakeBlobStore) SignedDownloadURL(ctx context.Context, objectKey string) (string, error) {
	return FakeDownloadURL(objectKey), nil
}

func (store *FakeBlobStore) ObjectSize(ctx context.Context, objectKey string) (int64, error) {
	store.lock.Lock()
	defer store.lock.Unlock()
	size, found := store.objectSizes[objectKey]
	if !found {
		return 0, blob.ErrObjectNotFound
	}
	return size, nil
}

func (store *FakeBlobStore) PutObject(objectKey string, sizeBytes int64) {
	store.lock.Lock()
	defer store.lock.Unlock()
	if store.objectSizes == nil {
		store.objectSizes = make(map[string]int64)
	}
	store.objectSizes[objectKey] = sizeBytes
}

func FakeDownloadURL(objectKey string) string {
	return fakeBlobAddress + "download/" + objectKey
}
