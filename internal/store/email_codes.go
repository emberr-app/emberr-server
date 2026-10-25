package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EmailCodeTable struct {
	pool *pgxpool.Pool
}

type ActiveEmailCode struct {
	CodeHash []byte
	Attempts int
}

func NewEmailCodeTable(pool *pgxpool.Pool) EmailCodeTable {
	return EmailCodeTable{pool: pool}
}

func (table EmailCodeTable) Save(ctx context.Context, emailHash string, purpose string, codeHash []byte, expiresAt time.Time) error {
	_, err := table.pool.Exec(ctx,
		`INSERT INTO email_codes (email_hash, purpose, code_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email_hash, purpose) DO UPDATE SET
			code_hash = EXCLUDED.code_hash,
			wrong_attempts = 0,
			created_at = EXCLUDED.created_at,
			expires_at = EXCLUDED.expires_at`,
		emailHash, purpose, codeHash, expiresAt.UnixMicro(),
	)
	if err != nil {
		return fmt.Errorf("save email code: %w", err)
	}
	return nil
}

func (table EmailCodeTable) AddAttempt(ctx context.Context, emailHash string, purpose string, now time.Time) (ActiveEmailCode, bool, error) {
	var activeCode ActiveEmailCode
	err := table.pool.QueryRow(ctx,
		`UPDATE email_codes SET wrong_attempts = wrong_attempts + 1
		WHERE email_hash = $1 AND purpose = $2 AND expires_at > $3
		RETURNING code_hash, wrong_attempts`,
		emailHash, purpose, now.UnixMicro(),
	).Scan(&activeCode.CodeHash, &activeCode.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActiveEmailCode{}, false, nil
	}
	if err != nil {
		return ActiveEmailCode{}, false, fmt.Errorf("count email code attempt: %w", err)
	}
	return activeCode, true, nil
}

func (table EmailCodeTable) Delete(ctx context.Context, emailHash string, purpose string, codeHash []byte) (bool, error) {
	result, err := table.pool.Exec(ctx,
		`DELETE FROM email_codes WHERE email_hash = $1 AND purpose = $2 AND code_hash = $3`,
		emailHash, purpose, codeHash,
	)
	if err != nil {
		return false, fmt.Errorf("delete email code: %w", err)
	}
	return result.RowsAffected() > 0, nil
}

func (table EmailCodeTable) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := table.pool.Exec(ctx, `DELETE FROM email_codes WHERE expires_at < now_utc_micro_seconds()`)
	if err != nil {
		return 0, fmt.Errorf("delete expired email codes: %w", err)
	}
	return result.RowsAffected(), nil
}
