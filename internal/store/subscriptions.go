package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const TrialPlan = "trial"

type SubscriptionTable struct {
	pool *pgxpool.Pool
}

type Trial struct {
	StorageLimitBytes int64
	Length            time.Duration
}

type Subscription struct {
	Plan               string `json:"plan"`
	StorageLimitBytes  int64  `json:"storageLimitBytes"`
	ExpiresAt          int64  `json:"expiresAt"`
	CloudCopyRemovedAt *int64 `json:"cloudCopyRemovedAt"`
}

type UploadAllowance struct {
	Active            bool
	StorageUsedBytes  int64
	StorageLimitBytes int64
}

type EndedTrial struct {
	AccountID           int64
	ExpiresAt           int64
	EncryptedEmail      []byte
	EncryptedEmailNonce []byte
	EmailsSentAt        map[string]int64
}

func NewSubscriptionTable(pool *pgxpool.Pool) SubscriptionTable {
	return SubscriptionTable{pool: pool}
}

func insertTrial(ctx context.Context, tx pgx.Tx, accountID int64, trial Trial) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO subscriptions (account_id, plan, storage_limit_bytes, expires_at) VALUES ($1, $2, $3, now_utc_micro_seconds() + $4)`,
		accountID, TrialPlan, trial.StorageLimitBytes, trial.Length.Microseconds(),
	)
	if err != nil {
		return fmt.Errorf("insert trial subscription: %w", err)
	}
	return nil
}

func (table SubscriptionTable) Find(ctx context.Context, accountID int64) (Subscription, error) {
	var subscription Subscription
	err := table.pool.QueryRow(ctx,
		`SELECT plan, storage_limit_bytes, expires_at, cloud_copy_removed_at FROM subscriptions WHERE account_id = $1`,
		accountID,
	).Scan(&subscription.Plan, &subscription.StorageLimitBytes, &subscription.ExpiresAt, &subscription.CloudCopyRemovedAt)
	if err != nil {
		return Subscription{}, fmt.Errorf("load subscription: %w", err)
	}
	return subscription, nil
}

func (table SubscriptionTable) FindUploadAllowance(ctx context.Context, accountID int64) (UploadAllowance, error) {
	var allowance UploadAllowance
	err := table.pool.QueryRow(ctx,
		`SELECT now_utc_micro_seconds() < subscriptions.expires_at, accounts.storage_used_bytes, subscriptions.storage_limit_bytes
		FROM accounts JOIN subscriptions ON subscriptions.account_id = accounts.account_id
		WHERE accounts.account_id = $1`,
		accountID,
	).Scan(&allowance.Active, &allowance.StorageUsedBytes, &allowance.StorageLimitBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return UploadAllowance{}, nil
	}
	if err != nil {
		return UploadAllowance{}, fmt.Errorf("load upload allowance: %w", err)
	}
	return allowance, nil
}

func (table SubscriptionTable) ListEndedTrialsWithCloudCopy(ctx context.Context) ([]EndedTrial, error) {
	rows, err := table.pool.Query(ctx,
		`SELECT subscriptions.account_id, subscriptions.expires_at, accounts.encrypted_email, accounts.encrypted_email_nonce,
			coalesce(array_agg(subscription_emails.kind) FILTER (WHERE subscription_emails.kind IS NOT NULL), '{}'),
			coalesce(array_agg(subscription_emails.sent_at) FILTER (WHERE subscription_emails.kind IS NOT NULL), '{}')
		FROM subscriptions
		JOIN accounts ON accounts.account_id = subscriptions.account_id
		LEFT JOIN subscription_emails ON subscription_emails.account_id = subscriptions.account_id
		WHERE subscriptions.plan = $1 AND subscriptions.cloud_copy_removed_at IS NULL AND subscriptions.expires_at <= now_utc_micro_seconds()
		GROUP BY subscriptions.account_id, subscriptions.expires_at, accounts.encrypted_email, accounts.encrypted_email_nonce
		ORDER BY subscriptions.account_id`,
		TrialPlan,
	)
	if err != nil {
		return nil, fmt.Errorf("list ended trials: %w", err)
	}
	endedTrials, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (EndedTrial, error) {
		var endedTrial EndedTrial
		var sentKinds []string
		var sentTimes []int64
		if err := row.Scan(&endedTrial.AccountID, &endedTrial.ExpiresAt, &endedTrial.EncryptedEmail, &endedTrial.EncryptedEmailNonce, &sentKinds, &sentTimes); err != nil {
			return EndedTrial{}, err
		}
		endedTrial.EmailsSentAt = make(map[string]int64, len(sentKinds))
		for index, kind := range sentKinds {
			endedTrial.EmailsSentAt[kind] = sentTimes[index]
		}
		return endedTrial, nil
	})
	if err != nil {
		return nil, fmt.Errorf("read ended trials: %w", err)
	}
	return endedTrials, nil
}

func (table SubscriptionTable) RecordEmailSent(ctx context.Context, accountID int64, kind string) error {
	_, err := table.pool.Exec(ctx,
		`INSERT INTO subscription_emails (account_id, kind, sent_at) VALUES ($1, $2, now_utc_micro_seconds())
		ON CONFLICT (account_id, kind) DO NOTHING`,
		accountID, kind,
	)
	if err != nil {
		return fmt.Errorf("record subscription email: %w", err)
	}
	return nil
}
