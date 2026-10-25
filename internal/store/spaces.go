package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SpaceTable struct {
	pool *pgxpool.Pool
}

type NewSpace struct {
	OwnerAccountID    int64
	ClientSpaceID     string
	Kind              string
	EncryptedKey      string
	EncryptedKeyNonce string
}

type Space struct {
	SpaceRef          int64  `json:"spaceRef"`
	ClientSpaceID     string `json:"clientSpaceId"`
	Kind              string `json:"kind"`
	LatestRevision    int64  `json:"latestRevision"`
	EncryptedKey      string `json:"encryptedKey"`
	EncryptedKeyNonce string `json:"encryptedKeyNonce"`
}

type SpaceRevisions struct {
	LatestRevision int64
	PrunedRevision int64
}

type Item struct {
	ItemID             string  `json:"itemId"`
	ItemType           string  `json:"itemType"`
	PayloadVersion     int32   `json:"payloadVersion"`
	ParentID           *string `json:"parentId"`
	EncryptedKey       *string `json:"encryptedKey"`
	EncryptedKeyNonce  *string `json:"encryptedKeyNonce"`
	InlinePayload      *string `json:"inlinePayload"`
	InlinePayloadNonce *string `json:"inlinePayloadNonce"`
	BlobObjectKey      *string `json:"-"`
	BlobNonce          *string `json:"blobNonce"`
	DownloadURL        *string `json:"downloadUrl"`
	Revision           int64   `json:"revision"`
	SizeBytes          int64   `json:"sizeBytes"`
	Deleted            bool    `json:"deleted"`
	UpdatedAt          int64   `json:"updatedAt"`
}

type LockedSpace struct {
	tx             pgx.Tx
	spaceRef       int64
	ownerAccountID int64
	latestRevision int64
}

const spaceColumns = `space_ref, client_space_id, kind, latest_revision, encrypted_key, encrypted_key_nonce`

const itemColumns = `item_id, item_type, payload_version, parent_id,
	encrypted_key, encrypted_key_nonce, inline_payload, inline_payload_nonce,
	blob_object_key, blob_nonce, revision, size_bytes, deleted, updated_at`

func spaceScanTargets(space *Space) []any {
	return []any{&space.SpaceRef, &space.ClientSpaceID, &space.Kind, &space.LatestRevision, &space.EncryptedKey, &space.EncryptedKeyNonce}
}

func itemScanTargets(item *Item) []any {
	return []any{
		&item.ItemID, &item.ItemType, &item.PayloadVersion, &item.ParentID,
		&item.EncryptedKey, &item.EncryptedKeyNonce, &item.InlinePayload, &item.InlinePayloadNonce,
		&item.BlobObjectKey, &item.BlobNonce, &item.Revision, &item.SizeBytes, &item.Deleted, &item.UpdatedAt,
	}
}

func NewSpaceTable(pool *pgxpool.Pool) SpaceTable {
	return SpaceTable{pool: pool}
}

func (table SpaceTable) Register(ctx context.Context, space NewSpace) (Space, error) {
	_, err := table.pool.Exec(ctx,
		`INSERT INTO spaces (owner_account_id, client_space_id, kind, encrypted_key, encrypted_key_nonce)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (owner_account_id, client_space_id) DO NOTHING`,
		space.OwnerAccountID, space.ClientSpaceID, space.Kind, space.EncryptedKey, space.EncryptedKeyNonce,
	)
	if err != nil {
		return Space{}, fmt.Errorf("insert space: %w", err)
	}

	var storedSpace Space
	err = table.pool.QueryRow(ctx,
		`SELECT `+spaceColumns+` FROM spaces WHERE owner_account_id = $1 AND client_space_id = $2`,
		space.OwnerAccountID, space.ClientSpaceID,
	).Scan(spaceScanTargets(&storedSpace)...)
	if err != nil {
		return Space{}, fmt.Errorf("load registered space: %w", err)
	}
	return storedSpace, nil
}

func (table SpaceTable) ListForOwner(ctx context.Context, ownerAccountID int64) ([]Space, error) {
	rows, err := table.pool.Query(ctx,
		`SELECT `+spaceColumns+` FROM spaces WHERE owner_account_id = $1 ORDER BY space_ref`,
		ownerAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}
	spaces, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Space, error) {
		var space Space
		err := row.Scan(spaceScanTargets(&space)...)
		return space, err
	})
	if err != nil {
		return nil, fmt.Errorf("read spaces: %w", err)
	}
	return spaces, nil
}

func (table SpaceTable) FindRevisions(ctx context.Context, ownerAccountID int64, spaceRef int64) (SpaceRevisions, bool, error) {
	var revisions SpaceRevisions
	err := table.pool.QueryRow(ctx,
		`SELECT latest_revision, pruned_revision FROM spaces WHERE space_ref = $1 AND owner_account_id = $2`,
		spaceRef, ownerAccountID,
	).Scan(&revisions.LatestRevision, &revisions.PrunedRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpaceRevisions{}, false, nil
	}
	if err != nil {
		return SpaceRevisions{}, false, fmt.Errorf("load space revisions: %w", err)
	}
	return revisions, true, nil
}

func (table SpaceTable) ListChangedItems(ctx context.Context, spaceRef int64, afterRevision int64, upToRevision int64, maxCount int) ([]Item, error) {
	rows, err := table.pool.Query(ctx,
		`SELECT `+itemColumns+` FROM items
		WHERE space_ref = $1 AND revision > $2 AND revision <= $3
		ORDER BY revision
		LIMIT $4`,
		spaceRef, afterRevision, upToRevision, maxCount,
	)
	if err != nil {
		return nil, fmt.Errorf("list changed items: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
		var item Item
		err := row.Scan(itemScanTargets(&item)...)
		return item, err
	})
	if err != nil {
		return nil, fmt.Errorf("read changed items: %w", err)
	}
	return items, nil
}

func (table SpaceTable) UpdateLocked(ctx context.Context, ownerAccountID int64, spaceRef int64, change func(space *LockedSpace) error) (bool, error) {
	found := false
	err := pgx.BeginFunc(ctx, table.pool, func(tx pgx.Tx) error {
		space := LockedSpace{tx: tx, spaceRef: spaceRef, ownerAccountID: ownerAccountID}
		err := tx.QueryRow(ctx,
			`SELECT latest_revision FROM spaces WHERE space_ref = $1 AND owner_account_id = $2 FOR UPDATE`,
			spaceRef, ownerAccountID,
		).Scan(&space.latestRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock space: %w", err)
		}
		found = true
		return change(&space)
	})
	if err != nil {
		return false, fmt.Errorf("update space: %w", err)
	}
	return found, nil
}

func (space *LockedSpace) FindItem(ctx context.Context, itemID string) (Item, bool, error) {
	var item Item
	err := space.tx.QueryRow(ctx,
		`SELECT `+itemColumns+` FROM items WHERE space_ref = $1 AND item_id = $2`,
		space.spaceRef, itemID,
	).Scan(itemScanTargets(&item)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, fmt.Errorf("load item: %w", err)
	}
	return item, true, nil
}

func (space *LockedSpace) SaveItemAsNextRevision(ctx context.Context, item Item) (int64, error) {
	nextRevision := space.latestRevision + 1
	_, err := space.tx.Exec(ctx,
		`INSERT INTO items (
			space_ref, item_id, item_type, payload_version, parent_id,
			encrypted_key, encrypted_key_nonce, inline_payload, inline_payload_nonce,
			blob_object_key, blob_nonce, revision, size_bytes, deleted, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now_utc_micro_seconds())
		ON CONFLICT (space_ref, item_id) DO UPDATE SET
			item_type = EXCLUDED.item_type,
			payload_version = EXCLUDED.payload_version,
			parent_id = EXCLUDED.parent_id,
			encrypted_key = EXCLUDED.encrypted_key,
			encrypted_key_nonce = EXCLUDED.encrypted_key_nonce,
			inline_payload = EXCLUDED.inline_payload,
			inline_payload_nonce = EXCLUDED.inline_payload_nonce,
			blob_object_key = EXCLUDED.blob_object_key,
			blob_nonce = EXCLUDED.blob_nonce,
			revision = EXCLUDED.revision,
			size_bytes = EXCLUDED.size_bytes,
			deleted = EXCLUDED.deleted,
			updated_at = EXCLUDED.updated_at`,
		space.spaceRef, item.ItemID, item.ItemType, item.PayloadVersion, item.ParentID,
		item.EncryptedKey, item.EncryptedKeyNonce, item.InlinePayload, item.InlinePayloadNonce,
		item.BlobObjectKey, item.BlobNonce, nextRevision, item.SizeBytes, item.Deleted,
	)
	if err != nil {
		return 0, fmt.Errorf("save item: %w", err)
	}

	if err := space.raiseLatestRevisionTo(ctx, nextRevision); err != nil {
		return 0, err
	}
	return nextRevision, nil
}

func (space *LockedSpace) ChangeStorageUsed(ctx context.Context, changeBytes int64) error {
	_, err := space.tx.Exec(ctx,
		`UPDATE accounts SET storage_used_bytes = storage_used_bytes + $2 WHERE account_id = $1`,
		space.ownerAccountID, changeBytes,
	)
	if err != nil {
		return fmt.Errorf("update storage used: %w", err)
	}
	return nil
}

func (space *LockedSpace) raiseLatestRevisionTo(ctx context.Context, revision int64) error {
	_, err := space.tx.Exec(ctx, `UPDATE spaces SET latest_revision = $2 WHERE space_ref = $1`, space.spaceRef, revision)
	if err != nil {
		return fmt.Errorf("raise space revision: %w", err)
	}
	space.latestRevision = revision
	return nil
}
