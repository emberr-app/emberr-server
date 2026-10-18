package store

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
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

	migrator, err := newMigrator(pool, migrationFiles)
	if err != nil {
		return err
	}
	defer migrator.Close()

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	slog.Info("database migrations up to date")
	return nil
}

func newMigrator(pool *pgxpool.Pool, migrationFiles source.Driver) (*migrate.Migrate, error) {
	database := stdlib.OpenDBFromPool(pool)
	databaseDriver, err := migratepgx.WithInstance(database, &migratepgx.Config{})
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("prepare migration driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", migrationFiles, "pgx5", databaseDriver)
	if err != nil {
		databaseDriver.Close()
		return nil, fmt.Errorf("create migrator: %w", err)
	}
	return migrator, nil
}
