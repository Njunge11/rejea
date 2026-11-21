package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
)

// The SQL migrations are compiled into the binary, so the binary alone can
// migrate a database.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// River keeps its own tables and its own migrations. A Go migration in this
// list brings them to a fixed version. When a River release adds a
// migration, add a new Go migration with the new version: do not change
// this one.
const (
	riverMigrationVersion = 2 // The place of the River tables in the list.
	riverSchemaVersion    = 8 // The version of the River tables.
)

// Migrate applies each migration that the database does not have. It gives
// the number of migrations that it applied. A session lock makes it safe
// when more than one process starts at the same time.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck // This does not close the pool.

	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return 0, fmt.Errorf("read the migration files: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return 0, fmt.Errorf("make the migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files,
		goose.WithSessionLocker(locker),
		goose.WithGoMigrations(goose.NewGoMigration(riverMigrationVersion,
			&goose.GoFunc{RunDB: riverUp},
			&goose.GoFunc{RunDB: riverDown},
		)),
	)
	if err != nil {
		return 0, fmt.Errorf("load the migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return len(results), fmt.Errorf("apply the migrations: %w", err)
	}
	return len(results), nil
}

// MigrationsHash gives a value that changes when the set of migrations
// changes. A database that was migrated with one hash has the same schema as
// each other database with that hash. When you add a Go migration, add its
// values to the hash.
func MigrationsHash() (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(migrationFiles, "migrations", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := migrationFiles.ReadFile(path)
		if err != nil {
			return err
		}
		hash.Write(fmt.Appendf(nil, "%s %d\n", path, len(content)))
		hash.Write(content)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("read the migration files: %w", err)
	}
	hash.Write(fmt.Appendf(nil, "river %d %d\n", riverMigrationVersion, riverSchemaVersion))
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func riverUp(ctx context.Context, db *sql.DB) error {
	migrator, err := rivermigrate.New(riverdatabasesql.New(db), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, &rivermigrate.MigrateOpts{
		TargetVersion: riverSchemaVersion,
	})
	return err
}

func riverDown(ctx context.Context, db *sql.DB) error {
	migrator, err := rivermigrate.New(riverdatabasesql.New(db), nil)
	if err != nil {
		return err
	}
	// Version -1 removes all River tables.
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionDown, &rivermigrate.MigrateOpts{
		TargetVersion: -1,
	})
	return err
}
