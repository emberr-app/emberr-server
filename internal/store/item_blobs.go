package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type PendingUpload struct {
	ObjectKey string
	AccountID int64
	SpaceRef  int64
	ItemID    string
	SizeBytes int64
	ExpiresAt int64
}

func (table SpaceTable) AddPendingUpload(ctx context.Context, upload PendingUpload) error {
	_, err := table.pool.Exec(ctx,
		`INSERT INTO pending_uploads (object_key, account_id, space_ref, item_id, size_bytes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		upload.ObjectKey, upload.AccountID, upload.SpaceRef, upload.ItemID, upload.SizeBytes, upload.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert pending upload: %w", err)
	}
	return nil
}

func (table SpaceTable) FindPendingUpload(ctx context.Context, objectKey string) (PendingUpload, bool, error) {
	upload := PendingUpload{ObjectKey: objectKey}
	err := table.pool.QueryRow(ctx,
		`SELECT account_id, space_ref, item_id, size_bytes, expires_at FROM pending_uploads WHERE object_key = $1`,
		objectKey,
	).Scan(&upload.AccountID, &upload.SpaceRef, &upload.ItemID, &upload.SizeBytes, &upload.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingUpload{}, false, nil
	}
	if err != nil {
		return PendingUpload{}, false, fmt.Errorf("load pending upload: %w", err)
	}
	return upload, true, nil
}

func (space LockedSpace) TakePendingUpload(ctx context.Context, objectKey string, itemID string, sizeBytes int64) (bool, error) {
	var takenObjectKey string
	err := space.tx.QueryRow(ctx,
		`DELETE FROM pending_uploads
		WHERE object_key = $1 AND account_id = $2 AND space_ref = $3 AND item_id = $4 AND size_bytes = $5
			AND expires_at > now_utc_micro_seconds()
		RETURNING object_key`,
		objectKey, space.ownerAccountID, space.spaceRef, itemID, sizeBytes,
	).Scan(&takenObjectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("take pending upload: %w", err)
	}
	return true, nil
}

func (space LockedSpace) QueueObjectDeletion(ctx context.Context, objectKey string, delay time.Duration) error {
	_, err := space.tx.Exec(ctx,
		`INSERT INTO object_deletions (object_key, delete_after) VALUES ($1, now_utc_micro_seconds() + $2)
		ON CONFLICT (object_key) DO NOTHING`,
		objectKey, delay.Microseconds(),
	)
	if err != nil {
		return fmt.Errorf("queue object deletion: %w", err)
	}
	return nil
}
