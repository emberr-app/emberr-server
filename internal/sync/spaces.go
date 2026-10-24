package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/store"
)

var (
	ErrSpaceNotFound = errors.New("space not found")
	ErrItemNotFound  = errors.New("item not found")
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

type Spaces struct {
	spaceTable store.SpaceTable
}

func NewSpaces(pool *pgxpool.Pool) *Spaces {
	return &Spaces{spaceTable: store.NewSpaceTable(pool)}
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
	if len(items) <= limit {
		return ChangePage{Items: items, Latest: revisions.LatestRevision}, nil
	}
	pageItems := items[:limit]
	return ChangePage{Items: pageItems, Latest: pageItems[limit-1].Revision, HasMore: true}, nil
}

func (spaces *Spaces) CommitItem(ctx context.Context, accountID int64, spaceRef int64, baseRevision int64, item store.Item) (int64, error) {
	var newRevision int64
	found, err := spaces.spaceTable.UpdateLocked(ctx, accountID, spaceRef, func(space store.LockedSpace) error {
		currentItem, exists, err := space.FindItem(ctx, item.ItemID)
		if err != nil {
			return err
		}
		if err := checkBaseRevision(currentItem, exists, baseRevision); err != nil {
			return err
		}

		item.Deleted = false
		newRevision, err = space.SaveItemAsNextRevision(ctx, item, item.SizeBytes-currentItem.SizeBytes)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("commit item: %w", err)
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
		return 0, fmt.Errorf("delete item: %w", err)
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
