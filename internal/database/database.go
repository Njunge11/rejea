// Package database connects to PostgreSQL and keeps its schema current.
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The reasons for which Connect fails. The caller tells the developer what
// to do for each one.
var (
	// ErrURL: the database URL does not have the form of a PostgreSQL URL.
	ErrURL = errors.New("the database URL is not valid")
	// ErrNoAnswer: no PostgreSQL server answers at the address of the URL.
	ErrNoAnswer = errors.New("no database answers")
	// ErrRefused: the server refuses the user or the password of the URL.
	ErrRefused = errors.New("the database refuses the user or the password")
	// ErrNoDatabase: the server has no database with the name of the URL.
	ErrNoDatabase = errors.New("the database server has no database with this name")
)

// The codes of PostgreSQL for the errors of a login.
const (
	codeWrongAuth     = "28000"
	codeWrongPassword = "28P01"
	codeNoDatabase    = "3D000"
)

// Connect opens a connection pool and confirms that the database answers.
// The caller closes the pool. An error has one of the reasons above.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrURL, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		reason := ErrNoAnswer
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.Code {
			case codeWrongAuth, codeWrongPassword:
				reason = ErrRefused
			case codeNoDatabase:
				reason = ErrNoDatabase
			}
		}
		return nil, fmt.Errorf("%w: %w", reason, err)
	}
	return pool, nil
}
