package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
	keys := &record.Keys
	err := table.pool.QueryRow(ctx,
		`SELECT accounts.account_id, login_key_hash, password_salt, mem_limit, ops_limit,
			encrypted_master_key, encrypted_master_key_nonce,
			master_key_encrypted_with_recovery_key, master_key_encrypted_with_recovery_key_nonce,
			recovery_key_encrypted_with_master_key, recovery_key_encrypted_with_master_key_nonce,
			public_key, encrypted_private_key, encrypted_private_key_nonce
		FROM accounts JOIN account_keys ON account_keys.account_id = accounts.account_id
		WHERE accounts.email_hash = $1`,
		emailHash,
	).Scan(
		&record.AccountID, &record.LoginKeyHash, &keys.PasswordSalt, &keys.MemLimit, &keys.OpsLimit,
		&keys.EncryptedMasterKey, &keys.EncryptedMasterKeyNonce,
		&keys.MasterKeyEncryptedWithRecoveryKey, &keys.MasterKeyEncryptedWithRecoveryKeyNonce,
		&keys.RecoveryKeyEncryptedWithMasterKey, &keys.RecoveryKeyEncryptedWithMasterKeyNonce,
		&keys.PublicKey, &keys.EncryptedPrivateKey, &keys.EncryptedPrivateKeyNonce,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginRecord{}, false, nil
	}
	if err != nil {
		return LoginRecord{}, false, fmt.Errorf("load login record: %w", err)
	}
	return record, true, nil
}
