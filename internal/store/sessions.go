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

type SessionTable struct {
	pool *pgxpool.Pool
}

type Session struct {
	AccountID  int64
	LastUsedAt time.Time
}

type SessionSummary struct {
	TokenHash  []byte
	DeviceName string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

type statementRunner interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func NewSessionTable(pool *pgxpool.Pool) SessionTable {
	return SessionTable{pool: pool}
}

func (table SessionTable) Create(ctx context.Context, tokenHash []byte, accountID int64, deviceName string) error {
	return insertSession(ctx, table.pool, tokenHash, accountID, deviceName)
}

func insertSession(ctx context.Context, database statementRunner, tokenHash []byte, accountID int64, deviceName string) error {
	_, err := database.Exec(ctx,
		`INSERT INTO sessions (token_hash, account_id, device_name) VALUES ($1, $2, $3)`,
		tokenHash, accountID, deviceName,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (table SessionTable) Find(ctx context.Context, tokenHash []byte) (Session, bool, error) {
	var session Session
	var lastUsedAtMicroseconds int64
	err := table.pool.QueryRow(ctx,
		`SELECT account_id, last_used_at FROM sessions WHERE token_hash = $1`,
		tokenHash,
	).Scan(&session.AccountID, &lastUsedAtMicroseconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("load session: %w", err)
	}
	session.LastUsedAt = time.UnixMicro(lastUsedAtMicroseconds)
	return session, true, nil
}

func (table SessionTable) MarkUsed(ctx context.Context, tokenHash []byte, usedAt time.Time) error {
	_, err := table.pool.Exec(ctx, `UPDATE sessions SET last_used_at = $2 WHERE token_hash = $1`, tokenHash, usedAt.UnixMicro())
	if err != nil {
		return fmt.Errorf("mark session used: %w", err)
	}
	return nil
}

func (table SessionTable) Delete(ctx context.Context, tokenHash []byte) error {
	_, err := table.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (table SessionTable) ListForAccount(ctx context.Context, accountID int64, usedSince time.Time) ([]SessionSummary, error) {
	rows, err := table.pool.Query(ctx,
		`SELECT token_hash, device_name, created_at, last_used_at FROM sessions
		WHERE account_id = $1 AND last_used_at >= $2
		ORDER BY created_at`,
		accountID, usedSince.UnixMicro(),
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	summaries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SessionSummary, error) {
		var summary SessionSummary
		var createdAtMicroseconds, lastUsedAtMicroseconds int64
		err := row.Scan(&summary.TokenHash, &summary.DeviceName, &createdAtMicroseconds, &lastUsedAtMicroseconds)
		summary.CreatedAt = time.UnixMicro(createdAtMicroseconds)
		summary.LastUsedAt = time.UnixMicro(lastUsedAtMicroseconds)
		return summary, err
	})
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	return summaries, nil
}

func (table SessionTable) DeleteForAccount(ctx context.Context, tokenHash []byte, accountID int64) (bool, error) {
	result, err := table.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1 AND account_id = $2`, tokenHash, accountID)
	if err != nil {
		return false, fmt.Errorf("delete session: %w", err)
	}
	return result.RowsAffected() > 0, nil
}
