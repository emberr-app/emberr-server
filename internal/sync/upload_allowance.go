package sync

import (
	"context"
	"errors"

	"github.com/emberr-app/emberr-server/internal/store"
)

const StorageOverflowAboveLimit = 50 << 20

var (
	ErrNoActiveSubscription = errors.New("no active subscription")
	ErrStorageLimitExceeded = errors.New("storage limit exceeded")
)

func checkUploadAllowed(ctx context.Context, subscriptionTable store.SubscriptionTable, accountID int64, newBytes int64) error {
	allowance, err := subscriptionTable.FindUploadAllowance(ctx, accountID)
	if err != nil {
		return err
	}
	if !allowance.Active {
		return ErrNoActiveSubscription
	}
	if newBytes > 0 && allowance.StorageUsedBytes+newBytes > allowance.StorageLimitBytes+StorageOverflowAboveLimit {
		return ErrStorageLimitExceeded
	}
	return nil
}
