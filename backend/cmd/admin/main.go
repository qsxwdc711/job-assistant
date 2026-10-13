package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"jobassistant/backend/internal/config"
	"jobassistant/backend/internal/database"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/migrations"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("admin command failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 || (os.Args[1] != "migrate" && os.Args[1] != "bootstrap") {
		return errors.New("usage: admin migrate|bootstrap")
	}
	dsn, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if os.Args[1] == "migrate" {
		if err := migrations.Apply(ctx, pool); err != nil {
			return err
		}
		slog.Info("database migrations applied")
		return nil
	}
	if err := migrations.Check(ctx, pool); err != nil {
		return err
	}
	service, err := identity.NewService(identity.NewStore(pool), identity.Options{})
	if err != nil {
		return err
	}
	u, err := service.Bootstrap(ctx, os.Getenv("ADMIN_USERNAME"), os.Getenv("ADMIN_PASSWORD"))
	if err != nil {
		return fmt.Errorf("bootstrap administrator: %w", err)
	}
	slog.Info("administrator initialized", "user_id", u.ID)
	return nil
}
