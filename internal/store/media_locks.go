package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type MediaLock struct {
	MediaID           string  `json:"mediaId"`
	NoteID            string  `json:"noteId"`
	EncryptedKey      *string `json:"encryptedKey"`
	EncryptedKeyNonce *string `json:"encryptedKeyNonce"`
	Revision          int64   `json:"revision"`
	Deleted           bool    `json:"deleted"`
	UpdatedAt         int64   `json:"updatedAt"`
}

const mediaLockColumns = `media_id, note_id, encrypted_key, encrypted_key_nonce, revision, deleted, updated_at`

func mediaLockScanTargets(lock *MediaLock) []any {
	return []any{&lock.MediaID, &lock.NoteID, &lock.EncryptedKey, &lock.EncryptedKeyNonce, &lock.Revision, &lock.Deleted, &lock.UpdatedAt}
}

func (table SpaceTable) ListChangedMediaLocks(ctx context.Context, spaceRef int64, afterRevision int64, upToRevision int64, maxCount int) ([]MediaLock, error) {
	rows, err := table.pool.Query(ctx,
		`SELECT `+mediaLockColumns+` FROM media_locks
		WHERE space_ref = $1 AND revision > $2 AND revision <= $3
		ORDER BY revision
		LIMIT $4`,
		spaceRef, afterRevision, upToRevision, maxCount,
	)
	if err != nil {
		return nil, fmt.Errorf("list changed media locks: %w", err)
	}
	locks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MediaLock, error) {
		var lock MediaLock
		err := row.Scan(mediaLockScanTargets(&lock)...)
		return lock, err
	})
	if err != nil {
		return nil, fmt.Errorf("read changed media locks: %w", err)
	}
	return locks, nil
}

func (space *LockedSpace) LockCommittedMediaFile(ctx context.Context, mediaID string) (bool, error) {
	var lockedMediaID string
	err := space.tx.QueryRow(ctx,
		`SELECT media_id FROM media_files WHERE media_id = $1 AND account_id = $2 AND committed_at IS NOT NULL FOR UPDATE`,
		mediaID, space.ownerAccountID,
	).Scan(&lockedMediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock media file: %w", err)
	}
	return true, nil
}

func (space *LockedSpace) LockMediaFilesOfLiveNoteLocks(ctx context.Context, noteID string) ([]string, error) {
	rows, err := space.tx.Query(ctx,
		`SELECT media_id FROM media_files
		WHERE media_id IN (SELECT media_id FROM media_locks WHERE space_ref = $1 AND note_id = $2 AND deleted = FALSE)
		ORDER BY media_id
		FOR UPDATE`,
		space.spaceRef, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("lock media files of the note's locks: %w", err)
	}
	mediaIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read media files of the note's locks: %w", err)
	}
	return mediaIDs, nil
}

func (space *LockedSpace) FindMediaLock(ctx context.Context, mediaID string, noteID string) (MediaLock, bool, error) {
	var lock MediaLock
	err := space.tx.QueryRow(ctx,
		`SELECT `+mediaLockColumns+` FROM media_locks WHERE space_ref = $1 AND media_id = $2 AND note_id = $3`,
		space.spaceRef, mediaID, noteID,
	).Scan(mediaLockScanTargets(&lock)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaLock{}, false, nil
	}
	if err != nil {
		return MediaLock{}, false, fmt.Errorf("load media lock: %w", err)
	}
	return lock, true, nil
}

func (space *LockedSpace) SaveMediaLockAsNextRevision(ctx context.Context, lock MediaLock) (int64, error) {
	nextRevision := space.latestRevision + 1
	_, err := space.tx.Exec(ctx,
		`INSERT INTO media_locks (space_ref, media_id, note_id, encrypted_key, encrypted_key_nonce, revision, deleted, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now_utc_micro_seconds())
		ON CONFLICT (space_ref, media_id, note_id) DO UPDATE SET
			encrypted_key = EXCLUDED.encrypted_key,
			encrypted_key_nonce = EXCLUDED.encrypted_key_nonce,
			revision = EXCLUDED.revision,
			deleted = EXCLUDED.deleted,
			updated_at = EXCLUDED.updated_at`,
		space.spaceRef, lock.MediaID, lock.NoteID, lock.EncryptedKey, lock.EncryptedKeyNonce, nextRevision, lock.Deleted,
	)
	if err != nil {
		return 0, fmt.Errorf("save media lock: %w", err)
	}

	if err := space.raiseLatestRevisionTo(ctx, nextRevision); err != nil {
		return 0, err
	}
	return nextRevision, nil
}

func (space *LockedSpace) TombstoneLiveMediaLocksOfNote(ctx context.Context, noteID string) error {
	result, err := space.tx.Exec(ctx,
		`WITH numbered_live_locks AS (
			SELECT media_id, $3::bigint + row_number() OVER (ORDER BY media_id) AS tombstone_revision
			FROM media_locks
			WHERE space_ref = $1 AND note_id = $2 AND deleted = FALSE
		)
		UPDATE media_locks SET
			deleted = TRUE,
			encrypted_key = NULL,
			encrypted_key_nonce = NULL,
			revision = numbered_live_locks.tombstone_revision,
			updated_at = now_utc_micro_seconds()
		FROM numbered_live_locks
		WHERE media_locks.space_ref = $1 AND media_locks.note_id = $2 AND media_locks.media_id = numbered_live_locks.media_id`,
		space.spaceRef, noteID, space.latestRevision,
	)
	if err != nil {
		return fmt.Errorf("turn the note's media locks into tombstones: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	return space.raiseLatestRevisionTo(ctx, space.latestRevision+result.RowsAffected())
}

func (space *LockedSpace) MarkMediaFileUsed(ctx context.Context, mediaID string) error {
	_, err := space.tx.Exec(ctx, `UPDATE media_files SET unused_since = NULL WHERE media_id = $1`, mediaID)
	if err != nil {
		return fmt.Errorf("mark media file used: %w", err)
	}
	return nil
}

func (space *LockedSpace) MarkMediaFilesUnusedWhereNoLiveLockIsLeft(ctx context.Context, mediaIDs []string) error {
	_, err := space.tx.Exec(ctx,
		`UPDATE media_files SET unused_since = now_utc_micro_seconds()
		WHERE media_id = ANY($1)
			AND NOT EXISTS (SELECT 1 FROM media_locks WHERE media_locks.media_id = media_files.media_id AND media_locks.deleted = FALSE)`,
		mediaIDs,
	)
	if err != nil {
		return fmt.Errorf("mark media files unused: %w", err)
	}
	return nil
}
