package store

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/emberr-app/emberr-server/internal/testutil"
)

func TestRateLimitRefusesAfterTheLimitAndAllowsAgainAfterTheWindow(t *testing.T) {
	pool := testutil.OpenTestDatabase(t)
	runMigration(t, "up", newTestMigrator(t, pool).Up)
	rateLimits := NewRateLimitTable(pool)
	limitKey := "test:" + rand.Text()
	const limit = 3
	const window = time.Minute
	windowStart := time.Now()

	countAttempt := func(now time.Time) bool {
		t.Helper()
		allowed, err := rateLimits.CountAttempt(t.Context(), limitKey, limit, window, now)
		if err != nil {
			t.Fatal(err)
		}
		return allowed
	}

	for attempt := 1; attempt <= limit; attempt++ {
		if !countAttempt(windowStart) {
			t.Fatalf("attempt %d was refused, want allowed up to %d", attempt, limit)
		}
	}
	if countAttempt(windowStart.Add(window - time.Second)) {
		t.Fatalf("attempt %d inside the window was allowed, want refused", limit+1)
	}
	if !countAttempt(windowStart.Add(window)) {
		t.Fatal("first attempt after the window was refused, want allowed")
	}
}
