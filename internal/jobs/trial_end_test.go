package jobs

import (
	"crypto/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
	"github.com/emberr-app/emberr-server/internal/testutil"
)

const testTrialEmail = "trial@example.com"

var testEmailProtector = auth.NewEmailProtector([auth.EmailEncryptionKeyLength]byte{1}, [auth.EmailHashKeyLength]byte{2})

type testCloudCopy struct {
	itemBlobKey      string
	pendingUploadKey string
	mediaID          string
}

func TestTrialEndedTwentyFiveDaysAgoGetsOnlyTheDay23Email(t *testing.T) {
	trialEnd, pool, mailSender := newTestTrialEnd(t)
	trialEndedAt := time.Now().Add(-25 * oneDay)
	accountID := addEndedTrialAccount(t, pool, trialEndedAt)

	runTrialEnd(t, trialEnd)

	sentMails := mailSender.SentMails()
	if len(sentMails) != 1 || sentMails[0].ToEmail != testTrialEmail || sentMails[0].Subject != "Your Emberr Cloud copy will be removed in 7 days" {
		t.Fatalf("sent mails = %+v, want only the day 23 email", sentMails)
	}
	if wantDate := trialEndedAt.Add(30 * oneDay).UTC().Format(removalDateLayout); !strings.Contains(sentMails[0].Body, wantDate) {
		t.Errorf("email body %q does not give the removal date %s", sentMails[0].Body, wantDate)
	}
	if kinds := sentEmailKinds(t, pool, accountID); !slices.Equal(kinds, []string{"trial_ended_day_23"}) {
		t.Errorf("recorded emails = %v, want only trial_ended_day_23", kinds)
	}
	if cloudCopyRemoved(t, pool, accountID) {
		t.Error("the cloud copy was removed on day 25")
	}
}

func TestLateFinalWarningPushesTheRemovalBackADayThenTheCloudCopyIsEmptied(t *testing.T) {
	trialEnd, pool, mailSender := newTestTrialEnd(t)
	accountID := addEndedTrialAccount(t, pool, time.Now().Add(-30*oneDay-time.Hour))
	recordSentEmail(t, pool, accountID, "trial_ended_day_0", time.Now().Add(-30*oneDay))
	recordSentEmail(t, pool, accountID, "trial_ended_day_23", time.Now().Add(-7*oneDay))
	cloudCopy := addTestCloudCopy(t, pool, accountID)

	runTrialEnd(t, trialEnd)

	sentMails := mailSender.SentMails()
	if len(sentMails) != 1 || sentMails[0].Subject != "Your Emberr Cloud copy will be removed tomorrow" {
		t.Fatalf("sent mails = %+v, want only the day 29 email", sentMails)
	}
	if cloudCopyRemoved(t, pool, accountID) || queryNumber(t, pool, `SELECT count(*) FROM spaces`) != 1 {
		t.Fatal("the cloud copy was removed in the same run as the late day 29 email")
	}

	recordSentEmail(t, pool, accountID, finalWarningKind, time.Now().Add(-removalNoticeLength-time.Minute))
	runTrialEnd(t, trialEnd)

	if sentCount := len(mailSender.SentMails()); sentCount != 1 {
		t.Errorf("%d emails sent in all, want no new one in the removal run", sentCount)
	}
	for _, table := range []string{"spaces", "items", "media_locks", "media_files", "pending_uploads"} {
		if rowCount := queryNumber(t, pool, `SELECT count(*) FROM `+table); rowCount != 0 {
			t.Errorf("%d rows left in %s after the removal, want 0", rowCount, table)
		}
	}
	objectKeys := []string{cloudCopy.itemBlobKey, cloudCopy.pendingUploadKey, store.MediaPieceKey(cloudCopy.mediaID, testMediaUploadID, 0), store.MediaPieceKey(cloudCopy.mediaID, testMediaUploadID, 1)}
	for _, objectKey := range objectKeys {
		if deleteAfter, queued := deletionTimeOf(t, pool, objectKey); !queued || deleteAfter > time.Now().UnixMicro() {
			t.Errorf("object %s: queued = %v, delete after %d; want queued and ready now", objectKey, queued, deleteAfter)
		}
	}
	if storageUsed := queryNumber(t, pool, `SELECT storage_used_bytes FROM accounts WHERE account_id = $1`, accountID); storageUsed != 0 {
		t.Errorf("storage used after the removal = %d, want 0", storageUsed)
	}
	if !cloudCopyRemoved(t, pool, accountID) {
		t.Error("cloud_copy_removed_at is not set after the removal")
	}
	if queryNumber(t, pool, `SELECT count(*) FROM accounts`) != 1 || queryNumber(t, pool, `SELECT count(*) FROM sessions`) != 1 || queryNumber(t, pool, `SELECT count(*) FROM subscriptions`) != 1 {
		t.Error("the account, its session or its subscription row is gone after the removal, want all kept")
	}
}

func TestFailedTrialEndEmailIsNotRecorded(t *testing.T) {
	trialEnd, pool, mailSender := newTestTrialEnd(t)
	accountID := addEndedTrialAccount(t, pool, time.Now().Add(-time.Hour))
	mailSender.FailSendsTo(testTrialEmail)

	runTrialEnd(t, trialEnd)

	if kinds := sentEmailKinds(t, pool, accountID); len(kinds) != 0 {
		t.Fatalf("recorded emails after a failed send = %v, want none", kinds)
	}
}

func newTestTrialEnd(t *testing.T) (*TrialEnd, *pgxpool.Pool, *testutil.FakeMailSender) {
	t.Helper()

	pool := testutil.OpenThrowawayDatabase(t)
	if err := store.ApplyMigrations(pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	mailSender := &testutil.FakeMailSender{}
	return NewTrialEnd(pool, mailSender, testEmailProtector), pool, mailSender
}

func runTrialEnd(t *testing.T, trialEnd *TrialEnd) {
	t.Helper()

	if err := trialEnd.WarnAndRemoveEndedTrials(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func addEndedTrialAccount(t *testing.T, pool *pgxpool.Pool, trialEndedAt time.Time) int64 {
	t.Helper()

	encryptedEmail := testEmailProtector.Encrypt(testTrialEmail)
	var accountID int64
	err := pool.QueryRow(t.Context(),
		`INSERT INTO accounts (email_hash, encrypted_email, encrypted_email_nonce, storage_used_bytes) VALUES ($1, $2, $3, $4) RETURNING account_id`,
		testEmailProtector.Hash(testTrialEmail), encryptedEmail.Ciphertext, encryptedEmail.Nonce, 3*testObjectBytes,
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	_, err = pool.Exec(t.Context(),
		`INSERT INTO subscriptions (account_id, plan, storage_limit_bytes, expires_at) VALUES ($1, $2, $3, $4)`,
		accountID, store.TrialPlan, 100<<20, trialEndedAt.UnixMicro(),
	)
	if err != nil {
		t.Fatalf("insert trial: %v", err)
	}
	return accountID
}

func addTestCloudCopy(t *testing.T, pool *pgxpool.Pool, accountID int64) testCloudCopy {
	t.Helper()

	cloudCopy := testCloudCopy{itemBlobKey: "items/note/blob", pendingUploadKey: "items/note/pending", mediaID: "media_used"}
	var spaceRef int64
	err := pool.QueryRow(t.Context(),
		`INSERT INTO spaces (owner_account_id, client_space_id, kind, encrypted_key, encrypted_key_nonce) VALUES ($1, 'space', 'space', 'key', 'nonce') RETURNING space_ref`,
		accountID,
	).Scan(&spaceRef)
	if err != nil {
		t.Fatalf("insert space: %v", err)
	}
	_, err = pool.Exec(t.Context(),
		`INSERT INTO items (space_ref, item_id, item_type, payload_version, revision, size_bytes, blob_object_key) VALUES ($1, 'note', 'NOTE', 1, 1, $2, $3)`,
		spaceRef, testObjectBytes, cloudCopy.itemBlobKey,
	)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	addMediaFile(t, pool, accountID, cloudCopy.mediaID, time.Now().Add(time.Hour), true)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO media_locks (space_ref, media_id, note_id, encrypted_key, encrypted_key_nonce, revision) VALUES ($1, $2, 'note', 'key', 'nonce', 2)`,
		spaceRef, cloudCopy.mediaID,
	)
	if err != nil {
		t.Fatalf("insert media lock: %v", err)
	}
	err = store.NewSpaceTable(pool).AddPendingUpload(t.Context(), store.PendingUpload{
		ObjectKey: cloudCopy.pendingUploadKey,
		AccountID: accountID,
		SpaceRef:  spaceRef,
		ItemID:    "note",
		SizeBytes: testObjectBytes,
		ExpiresAt: time.Now().Add(time.Hour).UnixMicro(),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionTokenHash := make([]byte, 32)
	rand.Read(sessionTokenHash)
	if _, err := pool.Exec(t.Context(), `INSERT INTO sessions (token_hash, account_id, device_name) VALUES ($1, $2, 'Test phone')`, sessionTokenHash, accountID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return cloudCopy
}

func recordSentEmail(t *testing.T, pool *pgxpool.Pool, accountID int64, kind string, sentAt time.Time) {
	t.Helper()

	_, err := pool.Exec(t.Context(),
		`INSERT INTO subscription_emails (account_id, kind, sent_at) VALUES ($1, $2, $3)
		ON CONFLICT (account_id, kind) DO UPDATE SET sent_at = EXCLUDED.sent_at`,
		accountID, kind, sentAt.UnixMicro(),
	)
	if err != nil {
		t.Fatalf("record sent email: %v", err)
	}
}

func sentEmailKinds(t *testing.T, pool *pgxpool.Pool, accountID int64) []string {
	t.Helper()

	rows, err := pool.Query(t.Context(), `SELECT kind FROM subscription_emails WHERE account_id = $1 ORDER BY kind`, accountID)
	if err != nil {
		t.Fatalf("list sent emails: %v", err)
	}
	kinds, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read sent emails: %v", err)
	}
	return kinds
}

func cloudCopyRemoved(t *testing.T, pool *pgxpool.Pool, accountID int64) bool {
	t.Helper()

	var removedAt *int64
	if err := pool.QueryRow(t.Context(), `SELECT cloud_copy_removed_at FROM subscriptions WHERE account_id = $1`, accountID).Scan(&removedAt); err != nil {
		t.Fatalf("load cloud copy removed at: %v", err)
	}
	return removedAt != nil
}

func queryNumber(t *testing.T, pool *pgxpool.Pool, query string, arguments ...any) int64 {
	t.Helper()

	var number int64
	if err := pool.QueryRow(t.Context(), query, arguments...).Scan(&number); err != nil {
		t.Fatalf("run %q: %v", query, err)
	}
	return number
}
