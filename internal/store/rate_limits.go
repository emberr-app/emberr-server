package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type RateLimitTable struct {
	pool *pgxpool.Pool
}

func NewRateLimitTable(pool *pgxpool.Pool) RateLimitTable {
	return RateLimitTable{pool: pool}
}

func (table RateLimitTable) CountAttempt(ctx context.Context, limitKey string, limit int, window time.Duration, now time.Time) (bool, error) {
	var attemptCount int
	err := table.pool.QueryRow(ctx,
		`INSERT INTO rate_limits (limit_key, attempt_count, window_ends_at)
		VALUES ($1, 1, $3)
		ON CONFLICT (limit_key) DO UPDATE SET
			attempt_count = CASE WHEN rate_limits.window_ends_at <= $2 THEN 1 ELSE rate_limits.attempt_count + 1 END,
			window_ends_at = CASE WHEN rate_limits.window_ends_at <= $2 THEN EXCLUDED.window_ends_at ELSE rate_limits.window_ends_at END
		RETURNING attempt_count`,
		limitKey, now.UnixMicro(), now.Add(window).UnixMicro(),
	).Scan(&attemptCount)
	if err != nil {
		return false, fmt.Errorf("count rate limited attempt: %w", err)
	}
	return attemptCount <= limit, nil
}

func (table RateLimitTable) DeleteEnded(ctx context.Context) (int64, error) {
	result, err := table.pool.Exec(ctx, `DELETE FROM rate_limits WHERE window_ends_at < now_utc_micro_seconds()`)
	if err != nil {
		return 0, fmt.Errorf("delete ended rate limits: %w", err)
	}
	return result.RowsAffected(), nil
}
