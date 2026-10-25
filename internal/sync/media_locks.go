package sync

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/emberr-app/emberr-server/internal/store"
)

var mediaLockNoteTypes = []string{"NOTE", "DAILY"}

var (
	ErrNoteNotFound           = errors.New("note not found")
	ErrCommittedMediaNotFound = errors.New("committed media file not found")
	ErrMediaLockNotFound      = errors.New("media lock not found")
)

func (spaces *Spaces) AddMediaLock(ctx context.Context, accountID int64, spaceRef int64, lock store.MediaLock) (int64, error) {
	var revision int64
	found, err := spaces.spaceTable.UpdateLocked(ctx, accountID, spaceRef, func(space *store.LockedSpace) error {
		note, exists, err := space.FindItem(ctx, lock.NoteID)
		if err != nil {
			return err
		}
		if !exists || note.Deleted || !slices.Contains(mediaLockNoteTypes, note.ItemType) {
			return ErrNoteNotFound
		}
		fileFound, err := space.LockCommittedMediaFile(ctx, lock.MediaID)
		if err != nil {
			return err
		}
		if !fileFound {
			return ErrCommittedMediaNotFound
		}

		currentLock, exists, err := space.FindMediaLock(ctx, lock.MediaID, lock.NoteID)
		if err != nil {
			return err
		}
		if exists && !currentLock.Deleted {
			revision = currentLock.Revision
		} else {
			lock.Deleted = false
			revision, err = space.SaveMediaLockAsNextRevision(ctx, lock)
			if err != nil {
				return err
			}
		}
		return space.MarkMediaFileUsed(ctx, lock.MediaID)
	})
	if err != nil {
		return 0, fmt.Errorf("add media lock: %w", err)
	}
	if !found {
		return 0, ErrSpaceNotFound
	}
	return revision, nil
}

func (spaces *Spaces) RemoveMediaLock(ctx context.Context, accountID int64, spaceRef int64, mediaID string, noteID string) (int64, error) {
	var revision int64
	found, err := spaces.spaceTable.UpdateLocked(ctx, accountID, spaceRef, func(space *store.LockedSpace) error {
		currentLock, exists, err := space.FindMediaLock(ctx, mediaID, noteID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrMediaLockNotFound
		}
		if currentLock.Deleted {
			revision = currentLock.Revision
			return nil
		}
		if _, err := space.LockCommittedMediaFile(ctx, mediaID); err != nil {
			return err
		}

		revision, err = space.SaveMediaLockAsNextRevision(ctx, store.MediaLock{MediaID: mediaID, NoteID: noteID, Deleted: true})
		if err != nil {
			return err
		}
		return space.MarkMediaFilesUnusedWhereNoLiveLockIsLeft(ctx, []string{mediaID})
	})
	if err != nil {
		return 0, fmt.Errorf("remove media lock: %w", err)
	}
	if !found {
		return 0, ErrSpaceNotFound
	}
	return revision, nil
}
