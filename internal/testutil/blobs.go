package testutil

import (
	"context"
	"errors"
	"sync"

	"github.com/emberr-app/emberr-server/internal/blob"
)

const fakeBlobAddress = "https://blobs.test/"

var errFakeDeleteFailed = errors.New("fake blob store refused the delete")

type FakeBlobStore struct {
	lock               sync.Mutex
	objectSizes        map[string]int64
	keysThatFailDelete map[string]bool
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

func (store *FakeBlobStore) DeleteObject(ctx context.Context, objectKey string) error {
	store.lock.Lock()
	defer store.lock.Unlock()
	if store.keysThatFailDelete[objectKey] {
		return errFakeDeleteFailed
	}
	delete(store.objectSizes, objectKey)
	return nil
}

func (store *FakeBlobStore) FailDeletesOf(objectKey string) {
	store.lock.Lock()
	defer store.lock.Unlock()
	if store.keysThatFailDelete == nil {
		store.keysThatFailDelete = make(map[string]bool)
	}
	store.keysThatFailDelete[objectKey] = true
}

func (store *FakeBlobStore) HasObject(objectKey string) bool {
	store.lock.Lock()
	defer store.lock.Unlock()
	_, found := store.objectSizes[objectKey]
	return found
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
