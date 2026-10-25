package sync

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/blob"
	"github.com/emberr-app/emberr-server/internal/store"
)

const replacedBlobDeletionDelay = 7 * 24 * time.Hour

var (
	ErrSpaceNotFound          = errors.New("space not found")
	ErrItemNotFound           = errors.New("item not found")
	ErrUploadNotHandedOut     = errors.New("object key was not handed out for this item")
	ErrUploadedSizeMismatched = errors.New("uploaded object is missing or has another size")
)

type ConflictError struct {
	CurrentItem *store.Item
}

func (err *ConflictError) Error() string {
	return "base revision does not match the item's current revision"
}

type ChangePage struct {
	Items          []store.Item
	Latest         int64
	HasMore        bool
	ResyncRequired bool
}

type UploadURL struct {
	ObjectKey string
	URL       string
	ExpiresAt int64
}

type Spaces struct {
	spaceTable store.SpaceTable
	blobStore  blob.Store
}

func NewSpaces(pool *pgxpool.Pool, blobStore blob.Store) *Spaces {
	return &Spaces{spaceTable: store.NewSpaceTable(pool), blobStore: blobStore}
}

func (spaces *Spaces) Register(ctx context.Context, space store.NewSpace) (store.Space, error) {
	return spaces.spaceTable.Register(ctx, space)
}

func (spaces *Spaces) List(ctx context.Context, accountID int64) ([]store.Space, error) {
	return spaces.spaceTable.ListForOwner(ctx, accountID)
}

func (spaces *Spaces) Changes(ctx context.Context, accountID int64, spaceRef int64, sinceRevision int64, limit int) (ChangePage, error) {
	revisions, found, err := spaces.spaceTable.FindRevisions(ctx, accountID, spaceRef)
	if err != nil {
		return ChangePage{}, err
	}
	if !found {
		return ChangePage{}, ErrSpaceNotFound
	}
	if sinceRevision > 0 && sinceRevision < revisions.PrunedRevision {
		return ChangePage{ResyncRequired: true, Latest: revisions.LatestRevision}, nil
	}

	items, err := spaces.spaceTable.ListChangedItems(ctx, spaceRef, sinceRevision, revisions.LatestRevision, limit+1)
	if err != nil {
		return ChangePage{}, err
	}
	page := ChangePage{Items: items, Latest: revisions.LatestRevision}
	if len(items) > limit {
		page = ChangePage{Items: items[:limit], Latest: items[limit-1].Revision, HasMore: true}
	}
	for index := range page.Items {
		if err := spaces.addDownloadURL(ctx, &page.Items[index]); err != nil {
			return ChangePage{}, err
		}
	}
	return page, nil
}

func (spaces *Spaces) CreateUploadURL(ctx context.Context, accountID int64, spaceRef int64, itemID string, sizeBytes int64) (UploadURL, error) {
	_, found, err := spaces.spaceTable.FindRevisions(ctx, accountID, spaceRef)
	if err != nil {
		return UploadURL{}, err
	}
	if !found {
		return UploadURL{}, ErrSpaceNotFound
	}

	objectKey := fmt.Sprintf("items/%d/%s/%s", spaceRef, itemID, rand.Text())
	uploadURL, err := spaces.blobStore.SignedUploadURL(ctx, objectKey, sizeBytes)
	if err != nil {
		return UploadURL{}, err
	}
	signedAt := time.Now()
	err = spaces.spaceTable.AddPendingUpload(ctx, store.PendingUpload{
		ObjectKey: objectKey,
		AccountID: accountID,
		SpaceRef:  spaceRef,
		ItemID:    itemID,
		SizeBytes: sizeBytes,
		ExpiresAt: signedAt.Add(2 * blob.SignedURLLifetime).UnixMicro(),
	})
	if err != nil {
		return UploadURL{}, err
	}
	return UploadURL{ObjectKey: objectKey, URL: uploadURL, ExpiresAt: signedAt.Add(blob.SignedURLLifetime).UnixMicro()}, nil
}

func (spaces *Spaces) CommitItem(ctx context.Context, accountID int64, spaceRef int64, baseRevision int64, item store.Item) (int64, error) {
	if item.BlobObjectKey != nil {
		if err := spaces.checkUploadedBlob(ctx, accountID, spaceRef, item); err != nil {
			return 0, err
		}
	}

	var newRevision int64
	found, err := spaces.spaceTable.UpdateLocked(ctx, accountID, spaceRef, func(space store.LockedSpace) error {
		if item.BlobObjectKey != nil {
			taken, err := space.TakePendingUpload(ctx, *item.BlobObjectKey, item.ItemID, item.SizeBytes)
			if err != nil {
				return err
			}
			if !taken {
				return ErrUploadNotHandedOut
			}
		}
		currentItem, exists, err := space.FindItem(ctx, item.ItemID)
		if err != nil {
			return err
		}
		if err := checkBaseRevision(currentItem, exists, baseRevision); err != nil {
			return err
		}
		if err := queueBlobDeletion(ctx, space, currentItem); err != nil {
			return err
		}

		item.Deleted = false
		newRevision, err = space.SaveItemAsNextRevision(ctx, item, item.SizeBytes-currentItem.SizeBytes)
		return err
	})
	if err != nil {
		return 0, spaces.withConflictDownloadURL(ctx, fmt.Errorf("commit item: %w", err))
	}
	if !found {
		return 0, ErrSpaceNotFound
	}
	return newRevision, nil
}

func (spaces *Spaces) DeleteItem(ctx context.Context, accountID int64, spaceRef int64, itemID string, baseRevision int64) (int64, error) {
	var tombstoneRevision int64
	found, err := spaces.spaceTable.UpdateLocked(ctx, accountID, spaceRef, func(space store.LockedSpace) error {
		currentItem, exists, err := space.FindItem(ctx, itemID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrItemNotFound
		}
		if err := checkBaseRevision(currentItem, exists, baseRevision); err != nil {
			return err
		}
		if currentItem.Deleted {
			tombstoneRevision = currentItem.Revision
			return nil
		}
		if err := queueBlobDeletion(ctx, space, currentItem); err != nil {
			return err
		}

		tombstone := store.Item{
			ItemID:         currentItem.ItemID,
			ItemType:       currentItem.ItemType,
			PayloadVersion: currentItem.PayloadVersion,
			Deleted:        true,
		}
		tombstoneRevision, err = space.SaveItemAsNextRevision(ctx, tombstone, -currentItem.SizeBytes)
		return err
	})
	if err != nil {
		return 0, spaces.withConflictDownloadURL(ctx, fmt.Errorf("delete item: %w", err))
	}
	if !found {
		return 0, ErrSpaceNotFound
	}
	return tombstoneRevision, nil
}

func checkBaseRevision(currentItem store.Item, exists bool, baseRevision int64) error {
	if currentItem.Revision == baseRevision {
		return nil
	}
	if !exists {
		return &ConflictError{}
	}
	return &ConflictError{CurrentItem: &currentItem}
}

func (spaces *Spaces) checkUploadedBlob(ctx context.Context, accountID int64, spaceRef int64, item store.Item) error {
	_, found, err := spaces.spaceTable.FindRevisions(ctx, accountID, spaceRef)
	if err != nil {
		return err
	}
	if !found {
		return ErrSpaceNotFound
	}

	upload, found, err := spaces.spaceTable.FindPendingUpload(ctx, *item.BlobObjectKey)
	if err != nil {
		return err
	}
	if !found || upload.AccountID != accountID || upload.SpaceRef != spaceRef || upload.ItemID != item.ItemID {
		return ErrUploadNotHandedOut
	}
	if upload.SizeBytes != item.SizeBytes {
		return ErrUploadedSizeMismatched
	}

	storedSize, err := spaces.blobStore.ObjectSize(ctx, *item.BlobObjectKey)
	if errors.Is(err, blob.ErrObjectNotFound) {
		return ErrUploadedSizeMismatched
	}
	if err != nil {
		return err
	}
	if storedSize != item.SizeBytes {
		return ErrUploadedSizeMismatched
	}
	return nil
}

func queueBlobDeletion(ctx context.Context, space store.LockedSpace, currentItem store.Item) error {
	if currentItem.BlobObjectKey == nil {
		return nil
	}
	return space.QueueObjectDeletion(ctx, *currentItem.BlobObjectKey, replacedBlobDeletionDelay)
}

func (spaces *Spaces) addDownloadURL(ctx context.Context, item *store.Item) error {
	if item.Deleted || item.BlobObjectKey == nil {
		return nil
	}
	downloadURL, err := spaces.blobStore.SignedDownloadURL(ctx, *item.BlobObjectKey)
	if err != nil {
		return err
	}
	item.DownloadURL = &downloadURL
	return nil
}

func (spaces *Spaces) withConflictDownloadURL(ctx context.Context, err error) error {
	var conflictError *ConflictError
	if !errors.As(err, &conflictError) || conflictError.CurrentItem == nil {
		return err
	}
	if urlError := spaces.addDownloadURL(ctx, conflictError.CurrentItem); urlError != nil {
		return urlError
	}
	return err
}
