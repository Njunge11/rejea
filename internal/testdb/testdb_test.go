package testdb_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Njunge11/rejea/internal/testdb"
)

func TestNew(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	var names []string

	t.Run("each test gets the full schema and sees no rows of another test", func(t *testing.T) {
		for i := range 8 {
			t.Run(fmt.Sprintf("test %d", i), func(t *testing.T) {
				t.Parallel()
				pool := testdb.New(t)

				// The migrations add one type. A row of another test
				// would make this count more than 2.
				if _, err := pool.Exec(ctx,
					`insert into doc_types (id, description, property_schema) values ('sop', 'A procedure', '{}')`); err != nil {
					t.Fatal(err)
				}
				var name string
				var types int
				if err := pool.QueryRow(ctx,
					`select current_database(), count(*) from doc_types`).Scan(&name, &types); err != nil {
					t.Fatal(err)
				}
				if types != 2 {
					t.Errorf("the database has %d document types, want 2", types)
				}
				mu.Lock()
				names = append(names, name)
				mu.Unlock()
			})
		}
	})

	// The subtests above are complete here.
	t.Run("the database of a completed test is dropped", func(t *testing.T) {
		serverURL := os.Getenv("DATABASE_URL")
		if serverURL == "" {
			serverURL = "postgres://rejea:rejea@127.0.0.1:5433/rejea"
		}
		conn, err := pgx.Connect(ctx, serverURL)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx) //nolint:errcheck // The test is at its end.

		if len(names) != 8 {
			t.Fatalf("%d tests gave the name of their database, want 8", len(names))
		}
		var left int
		if err := conn.QueryRow(ctx,
			`select count(*) from pg_database where datname = any($1)`, names).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Errorf("%d of the 8 databases stayed", left)
		}
	})
}
