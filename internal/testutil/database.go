package testutil

import (
	"os"
	"strings"
	"testing"

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
