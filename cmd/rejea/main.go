// Command rejea is the Rejea service.
//
//	rejea migrate   brings the database schema up to date
//	rejea serve     runs the HTTP server
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rejea:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	if len(args) != 1 || (args[0] != "migrate" && args[0] != "serve") {
		return errors.New("usage: rejea migrate | rejea serve")
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if args[0] == "migrate" {
		applied, err := database.Migrate(ctx, pool)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "migrations applied: %d\n", applied)
		return err
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "listening on http://%s\n", listener.Addr()); err != nil {
		return err
	}
	return server.Run(ctx, listener, server.NewHandler(pool))
}
