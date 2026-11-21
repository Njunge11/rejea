// Package testdb gives each test its own database on the development
// PostgreSQL server.
//
// The first test of a run migrates one template database. Each test then
// gets a copy of the template, which PostgreSQL makes much faster than it
// runs the migrations. The name of the template has the hash of the
// migrations, so a changed migration makes a new template.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Njunge11/rejea/internal/database"
)

const (
	// The server of compose.yaml. DATABASE_URL replaces it when it is set.
	defaultServerURL = "postgres://rejea:rejea@127.0.0.1:5433/rejea"

	templatePrefix = "rejea_test_template_"
	databasePrefix = "rejea_test_db_"

	// Test processes of different packages run at the same time. This
	// advisory lock lets only one of them build the template.
	templateLockKey = 7352001
)

// The template of this process: found or built by the first call of New.
var template struct {
	once sync.Once
	name string
	err  error
}

// New gives a pool that is connected to a new database with the full schema
// and no rows of a test. The database is dropped when the test ends. Tests
// that call New can run in parallel.
//
// The test fails when the server does not answer: start it with
// "make db-up".
func New(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	ctx := context.Background()

	serverURL := os.Getenv("DATABASE_URL")
	if serverURL == "" {
		serverURL = defaultServerURL
	}
	admin, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		tb.Fatalf("connect to the PostgreSQL server (start it with \"make db-up\"): %v", err)
	}
	tb.Cleanup(func() { _ = admin.Close(ctx) })

	template.once.Do(func() {
		template.name, template.err = ensureTemplate(ctx, admin, serverURL)
	})
	if template.err != nil {
		tb.Fatalf("prepare the template database: %v", template.err)
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		tb.Fatal(err)
	}
	name := databasePrefix + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "create database "+quote(name)+" template "+quote(template.name)); err != nil {
		tb.Fatalf("copy the template database: %v", err)
	}
	tb.Cleanup(func() {
		if _, err := admin.Exec(ctx, "drop database "+quote(name)+" with (force)"); err != nil {
			tb.Errorf("drop the test database: %v", err)
		}
	})

	pool, err := connect(ctx, serverURL, name)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(pool.Close)
	return pool
}

// ensureTemplate gives the name of the template for the migrations of this
// build. It builds the template when the server does not have it.
func ensureTemplate(ctx context.Context, admin *pgx.Conn, serverURL string) (string, error) {
	hash, err := database.MigrationsHash()
	if err != nil {
		return "", err
	}
	name := templatePrefix + hash[:16]

	if _, err := admin.Exec(ctx, "select pg_advisory_lock($1)", templateLockKey); err != nil {
		return "", fmt.Errorf("lock: %w", err)
	}
	defer func() { _, _ = admin.Exec(ctx, "select pg_advisory_unlock($1)", templateLockKey) }()

	var exists bool
	if err := admin.QueryRow(ctx,
		"select exists (select from pg_database where datname = $1)", name).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}

	// The database gets the name of the template only after all migrations
	// are applied. Thus a template with that name is always complete.
	building := name + "_building"
	if _, err := admin.Exec(ctx, "drop database if exists "+quote(building)+" with (force)"); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, "create database "+quote(building)); err != nil {
		return "", err
	}
	if err := migrate(ctx, serverURL, building); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, "alter database "+quote(building)+" rename to "+quote(name)); err != nil {
		return "", err
	}

	// Remove the templates of older migrations.
	rows, err := admin.Query(ctx, "select datname from pg_database")
	if err != nil {
		return "", err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	for _, other := range names {
		if strings.HasPrefix(other, templatePrefix) && other != name {
			if _, err := admin.Exec(ctx, "drop database if exists "+quote(other)+" with (force)"); err != nil {
				return "", err
			}
		}
	}
	return name, nil
}

// migrate applies all migrations to the named database. It closes its
// connections before it returns: PostgreSQL refuses to copy a template that
// has a connection.
func migrate(ctx context.Context, serverURL, name string) error {
	pool, err := connect(ctx, serverURL, name)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = database.Migrate(ctx, pool)
	return err
}

func connect(ctx context.Context, serverURL, name string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(serverURL)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Database = name
	return pgxpool.NewWithConfig(ctx, cfg)
}

func quote(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
