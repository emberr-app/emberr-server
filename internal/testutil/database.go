package testutil

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const TestDatabaseNamePrefix = "emberr_test"

func OpenTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()

	var missingNames []string
	for _, name := range []string{"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD"} {
		if os.Getenv(name) == "" {
			missingNames = append(missingNames, name)
		}
	}
	if len(missingNames) > 0 {
		t.Fatalf("database tests need PGHOST, PGPORT, PGDATABASE, PGUSER and PGPASSWORD pointing at a test database whose name starts with %q; missing: %s",
			TestDatabaseNamePrefix, strings.Join(missingNames, ", "))
	}

	pool, err := pgxpool.New(t.Context(), "")
	if err != nil {
		t.Fatalf("configure test database pool: %v", err)
	}
	t.Cleanup(pool.Close)

	var databaseName string
	if err := pool.QueryRow(t.Context(), `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	if !strings.HasPrefix(databaseName, TestDatabaseNamePrefix) {
		t.Fatalf("refusing to run database tests against %q: the test database name must start with %q", databaseName, TestDatabaseNamePrefix)
	}
	return pool
}

func OpenThrowawayDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()

	testDatabase := OpenTestDatabase(t)
	databaseName := TestDatabaseNamePrefix + "_throwaway_" + strings.ToLower(rand.Text())
	quotedDatabaseName := pgx.Identifier{databaseName}.Sanitize()
	if _, err := testDatabase.Exec(t.Context(), `CREATE DATABASE `+quotedDatabaseName); err != nil {
		t.Fatalf("create throwaway database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testDatabase.Exec(context.Background(), `DROP DATABASE IF EXISTS `+quotedDatabaseName+` WITH (FORCE)`); err != nil {
			t.Errorf("drop throwaway database: %v", err)
		}
	})

	config := testDatabase.Config()
	config.ConnConfig.Database = databaseName
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("configure throwaway database pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
