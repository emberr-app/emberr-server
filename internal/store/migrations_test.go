package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/testutil"
)

const testMigrationsFolder = "../../migrations"

var accountTableNames = []string{"account_keys", "accounts", "email_codes", "items", "media_files", "media_locks", "object_deletions", "pending_uploads", "rate_limits", "sessions", "spaces", "subscription_emails", "subscriptions"}

func TestMigrationsCreateAndDropAccountTables(t *testing.T) {
	pool := testutil.OpenThrowawayDatabase(t)
	migrator := newTestMigrator(t, pool)

	runMigration(t, "up", migrator.Up)
	if tables := existingAccountTables(t, pool); !slices.Equal(tables, accountTableNames) {
		t.Fatalf("after up, tables = %v, want %v", tables, accountTableNames)
	}

	runMigration(t, "down", migrator.Down)
	if tables := existingAccountTables(t, pool); len(tables) != 0 {
		t.Fatalf("after down, tables %v still exist", tables)
	}

	runMigration(t, "up again", migrator.Up)
	if tables := existingAccountTables(t, pool); !slices.Equal(tables, accountTableNames) {
		t.Fatalf("after up again, tables = %v, want %v", tables, accountTableNames)
	}
}

func TestDeletingAnAccountRemovesItsKeysAndSessions(t *testing.T) {
	pool := testutil.OpenTestDatabase(t)
	runMigration(t, "up", newTestMigrator(t, pool).Up)
	ctx := t.Context()

	var accountID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO accounts (email_hash, encrypted_email, encrypted_email_nonce) VALUES ($1, $2, $3) RETURNING account_id`,
		"cascade-test-email-hash", []byte("encrypted email"), []byte("nonce"),
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM accounts WHERE account_id = $1`, accountID)
	})

	_, err = pool.Exec(ctx,
		`INSERT INTO account_keys (
			account_id, password_salt, mem_limit, ops_limit, login_key_hash,
			encrypted_master_key, encrypted_master_key_nonce,
			master_key_encrypted_with_recovery_key, master_key_encrypted_with_recovery_key_nonce,
			recovery_key_encrypted_with_master_key, recovery_key_encrypted_with_master_key_nonce,
			public_key, encrypted_private_key, encrypted_private_key_nonce
		) VALUES ($1, 'salt', 67108864, 3, 'hash', 'key', 'nonce', 'key', 'nonce', 'key', 'nonce', 'public', 'key', 'nonce')`,
		accountID,
	)
	if err != nil {
		t.Fatalf("insert account keys: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, account_id, device_name) VALUES ($1, $2, $3)`,
		bytes.Repeat([]byte{7}, 32), accountID, "Test phone",
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE account_id = $1`, accountID); err != nil {
		t.Fatalf("delete account: %v", err)
	}

	for _, childTable := range []string{"account_keys", "sessions"} {
		var remainingRows int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM `+childTable+` WHERE account_id = $1`, accountID).Scan(&remainingRows)
		if err != nil {
			t.Fatalf("count %s rows: %v", childTable, err)
		}
		if remainingRows != 0 {
			t.Errorf("%s still has %d rows for the deleted account", childTable, remainingRows)
		}
	}
}

func TestMigrationNineRecomputesStorageFromLiveItemsAndCountedFiles(t *testing.T) {
	pool := testutil.OpenThrowawayDatabase(t)
	migrator := newTestMigrator(t, pool)
	runMigration(t, "up to 8", func() error { return migrator.Migrate(8) })
	ctx := t.Context()

	var accountID, spaceRef int64
	err := pool.QueryRow(ctx,
		`INSERT INTO accounts (email_hash, encrypted_email, encrypted_email_nonce, storage_used_bytes) VALUES ('email-hash', 'email', 'nonce', 999999) RETURNING account_id`,
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO spaces (owner_account_id, client_space_id, kind, encrypted_key, encrypted_key_nonce) VALUES ($1, 'space', 'space', 'key', 'nonce') RETURNING space_ref`,
		accountID,
	).Scan(&spaceRef)
	if err != nil {
		t.Fatalf("insert space: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO items (space_ref, item_id, item_type, payload_version, revision, size_bytes) VALUES ($1, 'note', 'NOTE', 1, 1, 100)`,
		spaceRef,
	)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO media_files (media_id, account_id, upload_id, piece_sizes, size_bytes, expires_at, committed_at, unused_since) VALUES
			('media_used', $1, 'upload', '{1000}', 1000, 0, 1, NULL),
			('media_lost_its_lock', $1, 'upload', '{20000}', 20000, 0, 1, 2)`,
		accountID,
	)
	if err != nil {
		t.Fatalf("insert media files: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO media_locks (space_ref, media_id, note_id, encrypted_key, encrypted_key_nonce, revision, deleted) VALUES
			($1, 'media_used', 'note', 'key', 'nonce', 2, FALSE),
			($1, 'media_lost_its_lock', 'note', NULL, NULL, 3, TRUE)`,
		spaceRef,
	)
	if err != nil {
		t.Fatalf("insert media locks: %v", err)
	}

	runMigration(t, "up to 9", func() error { return migrator.Migrate(9) })

	var storageUsed int64
	if err := pool.QueryRow(ctx, `SELECT storage_used_bytes FROM accounts WHERE account_id = $1`, accountID).Scan(&storageUsed); err != nil {
		t.Fatalf("load storage used: %v", err)
	}
	if storageUsed != 1100 {
		t.Errorf("storage used = %d, want 1100 (the live item and the file still in use)", storageUsed)
	}
	var lostFileCounted bool
	if err := pool.QueryRow(ctx, `SELECT counted_in_storage FROM media_files WHERE media_id = 'media_lost_its_lock'`).Scan(&lostFileCounted); err != nil {
		t.Fatalf("load counted in storage: %v", err)
	}
	if lostFileCounted {
		t.Error("the file that lost its last lock still counts")
	}
}

func newTestMigrator(t *testing.T, pool *pgxpool.Pool) *migrate.Migrate {
	t.Helper()

	migrationFiles, err := iofs.New(os.DirFS(testMigrationsFolder), ".")
	if err != nil {
		t.Fatalf("read migrations folder: %v", err)
	}
	migrator, err := newMigrator(pool, migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { migrator.Close() })
	return migrator
}

func runMigration(t *testing.T, stepName string, apply func() error) {
	t.Helper()

	if err := apply(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate %s: %v", stepName, err)
	}
}

func existingAccountTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()

	rows, err := pool.Query(t.Context(),
		`SELECT table_name::text FROM information_schema.tables WHERE table_schema = current_schema() AND table_name::text = ANY($1::text[])`,
		accountTableNames,
	)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read table names: %v", err)
	}
	slices.Sort(tables)
	return tables
}
