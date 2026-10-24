package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountTable struct {
	pool *pgxpool.Pool
}

type AccountKeys struct {
	PasswordSalt                           string `json:"salt"`
	MemLimit                               int64  `json:"memLimit"`
	OpsLimit                               int32  `json:"opsLimit"`
	EncryptedMasterKey                     string `json:"encryptedMasterKey"`
	EncryptedMasterKeyNonce                string `json:"encryptedMasterKeyNonce"`
	MasterKeyEncryptedWithRecoveryKey      string `json:"masterKeyEncryptedWithRecoveryKey"`
	MasterKeyEncryptedWithRecoveryKeyNonce string `json:"masterKeyEncryptedWithRecoveryKeyNonce"`
	RecoveryKeyEncryptedWithMasterKey      string `json:"recoveryKeyEncryptedWithMasterKey"`
	RecoveryKeyEncryptedWithMasterKeyNonce string `json:"recoveryKeyEncryptedWithMasterKeyNonce"`
	PublicKey                              string `json:"publicKey"`
	EncryptedPrivateKey                    string `json:"encryptedPrivateKey"`
	EncryptedPrivateKeyNonce               string `json:"encryptedPrivateKeyNonce"`
}

type NewAccount struct {
	EmailHash           string
	EncryptedEmail      []byte
	EncryptedEmailNonce []byte
	LoginKeyHash        string
	Keys                AccountKeys
	SessionTokenHash    []byte
	DeviceName          string
}

type LoginRecord struct {
	AccountID    int64
	LoginKeyHash string
	Keys         AccountKeys
}

type Account struct {
	EncryptedEmail      []byte
	EncryptedEmailNonce []byte
	StorageUsedBytes    int64
	LoginKeyHash        string
	Keys                AccountKeys
}

type PasswordKeys struct {
	PasswordSalt            string `json:"salt"`
	MemLimit                int64  `json:"memLimit"`
	OpsLimit                int32  `json:"opsLimit"`
	EncryptedMasterKey      string `json:"encryptedMasterKey"`
	EncryptedMasterKeyNonce string `json:"encryptedMasterKeyNonce"`
}

type PasswordChange struct {
	AccountID                  int64
	LoginKeyHash               string
	Keys                       PasswordKeys
	SignOutOtherSessions       bool
	CurrentTokenHash           []byte
	UsesPasswordResetAllowance bool
	ChangedAt                  time.Time
}

const uniqueViolationCode = "23505"

var (
	ErrEmailHashTaken           = errors.New("email hash already belongs to an account")
	ErrNoPasswordResetAllowance = errors.New("session may not reset the password without a code")
)

const accountKeysColumns = `password_salt, mem_limit, ops_limit,
	encrypted_master_key, encrypted_master_key_nonce,
	master_key_encrypted_with_recovery_key, master_key_encrypted_with_recovery_key_nonce,
	recovery_key_encrypted_with_master_key, recovery_key_encrypted_with_master_key_nonce,
	public_key, encrypted_private_key, encrypted_private_key_nonce`

func accountKeysScanTargets(keys *AccountKeys) []any {
	return []any{
		&keys.PasswordSalt, &keys.MemLimit, &keys.OpsLimit,
		&keys.EncryptedMasterKey, &keys.EncryptedMasterKeyNonce,
		&keys.MasterKeyEncryptedWithRecoveryKey, &keys.MasterKeyEncryptedWithRecoveryKeyNonce,
		&keys.RecoveryKeyEncryptedWithMasterKey, &keys.RecoveryKeyEncryptedWithMasterKeyNonce,
		&keys.PublicKey, &keys.EncryptedPrivateKey, &keys.EncryptedPrivateKeyNonce,
	}
}

func NewAccountTable(pool *pgxpool.Pool) AccountTable {
	return AccountTable{pool: pool}
}

func (table AccountTable) ExistsForEmailHash(ctx context.Context, emailHash string) (bool, error) {
	var exists bool
	err := table.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE email_hash = $1)`, emailHash).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check account exists: %w", err)
	}
	return exists, nil
}

func (table AccountTable) CreateWithSession(ctx context.Context, account NewAccount) error {
	err := pgx.BeginFunc(ctx, table.pool, func(tx pgx.Tx) error {
		var accountID int64
		err := tx.QueryRow(ctx,
			`INSERT INTO accounts (email_hash, encrypted_email, encrypted_email_nonce) VALUES ($1, $2, $3) RETURNING account_id`,
			account.EmailHash, account.EncryptedEmail, account.EncryptedEmailNonce,
		).Scan(&accountID)
		if err != nil {
			return fmt.Errorf("insert account: %w", err)
		}

		keys := account.Keys
		_, err = tx.Exec(ctx,
			`INSERT INTO account_keys (
				account_id, password_salt, mem_limit, ops_limit, login_key_hash,
				encrypted_master_key, encrypted_master_key_nonce,
				master_key_encrypted_with_recovery_key, master_key_encrypted_with_recovery_key_nonce,
				recovery_key_encrypted_with_master_key, recovery_key_encrypted_with_master_key_nonce,
				public_key, encrypted_private_key, encrypted_private_key_nonce
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			accountID, keys.PasswordSalt, keys.MemLimit, keys.OpsLimit, account.LoginKeyHash,
			keys.EncryptedMasterKey, keys.EncryptedMasterKeyNonce,
			keys.MasterKeyEncryptedWithRecoveryKey, keys.MasterKeyEncryptedWithRecoveryKeyNonce,
			keys.RecoveryKeyEncryptedWithMasterKey, keys.RecoveryKeyEncryptedWithMasterKeyNonce,
			keys.PublicKey, keys.EncryptedPrivateKey, keys.EncryptedPrivateKeyNonce,
		)
		if err != nil {
			return fmt.Errorf("insert account keys: %w", err)
		}

		return insertSession(ctx, tx, account.SessionTokenHash, accountID, account.DeviceName)
	})
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

func (table AccountTable) FindLoginRecord(ctx context.Context, emailHash string) (LoginRecord, bool, error) {
	var record LoginRecord
	err := table.pool.QueryRow(ctx,
		`SELECT accounts.account_id, login_key_hash, `+accountKeysColumns+`
		FROM accounts JOIN account_keys ON account_keys.account_id = accounts.account_id
		WHERE accounts.email_hash = $1`,
		emailHash,
	).Scan(append([]any{&record.AccountID, &record.LoginKeyHash}, accountKeysScanTargets(&record.Keys)...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginRecord{}, false, nil
	}
	if err != nil {
		return LoginRecord{}, false, fmt.Errorf("load login record: %w", err)
	}
	return record, true, nil
}

func (table AccountTable) Find(ctx context.Context, accountID int64) (Account, bool, error) {
	var account Account
	err := table.pool.QueryRow(ctx,
		`SELECT encrypted_email, encrypted_email_nonce, storage_used_bytes, login_key_hash, `+accountKeysColumns+`
		FROM accounts JOIN account_keys ON account_keys.account_id = accounts.account_id
		WHERE accounts.account_id = $1`,
		accountID,
	).Scan(append([]any{&account.EncryptedEmail, &account.EncryptedEmailNonce, &account.StorageUsedBytes, &account.LoginKeyHash}, accountKeysScanTargets(&account.Keys)...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, fmt.Errorf("load account: %w", err)
	}
	return account, true, nil
}

func (table AccountTable) ChangePassword(ctx context.Context, change PasswordChange) error {
	err := pgx.BeginFunc(ctx, table.pool, func(tx pgx.Tx) error {
		if change.UsesPasswordResetAllowance {
			allowanceResult, err := tx.Exec(ctx,
				`UPDATE sessions SET password_reset_until = NULL
				WHERE token_hash = $1 AND account_id = $2 AND password_reset_until > $3`,
				change.CurrentTokenHash, change.AccountID, change.ChangedAt.UnixMicro(),
			)
			if err != nil {
				return fmt.Errorf("use password reset allowance: %w", err)
			}
			if allowanceResult.RowsAffected() == 0 {
				return ErrNoPasswordResetAllowance
			}
		}

		keys := change.Keys
		_, err := tx.Exec(ctx,
			`UPDATE account_keys SET
				password_salt = $2, mem_limit = $3, ops_limit = $4, login_key_hash = $5,
				encrypted_master_key = $6, encrypted_master_key_nonce = $7
			WHERE account_id = $1`,
			change.AccountID, keys.PasswordSalt, keys.MemLimit, keys.OpsLimit, change.LoginKeyHash,
			keys.EncryptedMasterKey, keys.EncryptedMasterKeyNonce,
		)
		if err != nil {
			return fmt.Errorf("update account keys: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE sessions SET password_reset_until = NULL WHERE account_id = $1 AND password_reset_until IS NOT NULL`, change.AccountID)
		if err != nil {
			return fmt.Errorf("clear password reset allowances: %w", err)
		}
		if !change.SignOutOtherSessions {
			return nil
		}

		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE account_id = $1 AND token_hash <> $2`, change.AccountID, change.CurrentTokenHash)
		if err != nil {
			return fmt.Errorf("sign out other sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	return nil
}

func (table AccountTable) UpdateEmail(ctx context.Context, accountID int64, emailHash string, encryptedEmail []byte, encryptedEmailNonce []byte) error {
	_, err := table.pool.Exec(ctx,
		`UPDATE accounts SET email_hash = $2, encrypted_email = $3, encrypted_email_nonce = $4 WHERE account_id = $1`,
		accountID, emailHash, encryptedEmail, encryptedEmailNonce,
	)
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == uniqueViolationCode {
		return ErrEmailHashTaken
	}
	if err != nil {
		return fmt.Errorf("update email: %w", err)
	}
	return nil
}

func (table AccountTable) Delete(ctx context.Context, accountID int64) error {
	err := pgx.BeginFunc(ctx, table.pool, func(tx pgx.Tx) error {
		if err := lockSpacesAndQueueEveryBlobForDeletionNow(ctx, tx, accountID); err != nil {
			return err
		}

		_, err := tx.Exec(ctx, `DELETE FROM accounts WHERE account_id = $1`, accountID)
		if err != nil {
			return fmt.Errorf("delete account row: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return nil
}

func (table AccountTable) EraseAllData(ctx context.Context, accountID int64) error {
	err := pgx.BeginFunc(ctx, table.pool, func(tx pgx.Tx) error {
		if err := lockSpacesAndQueueEveryBlobForDeletionNow(ctx, tx, accountID); err != nil {
			return err
		}

		_, err := tx.Exec(ctx,
			`WITH numbered_live_items AS (
				SELECT items.space_ref, items.item_id,
					spaces.latest_revision + row_number() OVER (PARTITION BY items.space_ref ORDER BY items.item_id) AS tombstone_revision
				FROM items JOIN spaces ON spaces.space_ref = items.space_ref
				WHERE spaces.owner_account_id = $1 AND items.deleted = FALSE
			),
			erased_items AS (
				UPDATE items SET
					deleted = TRUE,
					parent_id = NULL,
					encrypted_key = NULL,
					encrypted_key_nonce = NULL,
					inline_payload = NULL,
					inline_payload_nonce = NULL,
					blob_object_key = NULL,
					blob_nonce = NULL,
					size_bytes = 0,
					revision = numbered_live_items.tombstone_revision,
					updated_at = now_utc_micro_seconds()
				FROM numbered_live_items
				WHERE items.space_ref = numbered_live_items.space_ref AND items.item_id = numbered_live_items.item_id
				RETURNING items.space_ref
			),
			erased_counts AS (
				SELECT space_ref, count(*) AS erased_count FROM erased_items GROUP BY space_ref
			)
			UPDATE spaces SET latest_revision = spaces.latest_revision + erased_counts.erased_count
			FROM erased_counts
			WHERE spaces.space_ref = erased_counts.space_ref`,
			accountID,
		)
		if err != nil {
			return fmt.Errorf("turn items into tombstones: %w", err)
		}

		_, err = tx.Exec(ctx, `UPDATE accounts SET storage_used_bytes = 0 WHERE account_id = $1`, accountID)
		if err != nil {
			return fmt.Errorf("reset storage used: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("erase account data: %w", err)
	}
	return nil
}

func lockSpacesAndQueueEveryBlobForDeletionNow(ctx context.Context, tx pgx.Tx, accountID int64) error {
	_, err := tx.Exec(ctx, `SELECT space_ref FROM spaces WHERE owner_account_id = $1 ORDER BY space_ref FOR UPDATE`, accountID)
	if err != nil {
		return fmt.Errorf("lock spaces: %w", err)
	}

	_, err = tx.Exec(ctx,
		`WITH removed_uploads AS (
			DELETE FROM pending_uploads WHERE account_id = $1 RETURNING object_key
		)
		INSERT INTO object_deletions (object_key, delete_after)
		SELECT object_key, now_utc_micro_seconds() FROM removed_uploads
		ON CONFLICT (object_key) DO NOTHING`,
		accountID,
	)
	if err != nil {
		return fmt.Errorf("queue pending upload deletions: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO object_deletions (object_key, delete_after)
		SELECT items.blob_object_key, now_utc_micro_seconds()
		FROM items JOIN spaces ON spaces.space_ref = items.space_ref
		WHERE spaces.owner_account_id = $1 AND items.blob_object_key IS NOT NULL
		ON CONFLICT (object_key) DO NOTHING`,
		accountID,
	)
	if err != nil {
		return fmt.Errorf("queue item blob deletions: %w", err)
	}
	return nil
}
