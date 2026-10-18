package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountTable struct {
	pool *pgxpool.Pool
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
