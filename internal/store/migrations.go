package store

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

func ApplyMigrations(pool *pgxpool.Pool, migrationsFolder string) error {
	migrationFiles, err := iofs.New(os.DirFS(migrationsFolder), ".")
	if err != nil {
		return fmt.Errorf("read migrations folder %q: %w", migrationsFolder, err)
	}
	if _, err := migrationFiles.First(); errors.Is(err, fs.ErrNotExist) {
		slog.Info("no migrations to apply", "folder", migrationsFolder)
		return nil
	}

	database := stdlib.OpenDBFromPool(pool)
	databaseDriver, err := migratepgx.WithInstance(database, &migratepgx.Config{})
	if err != nil {
		database.Close()
		return fmt.Errorf("prepare migration driver: %w", err)
	}
	defer databaseDriver.Close()

	migrator, err := migrate.NewWithInstance("iofs", migrationFiles, "pgx5", databaseDriver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	slog.Info("database migrations up to date")
	return nil
}
