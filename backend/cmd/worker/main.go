package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"jobassistant/backend/internal/config"
	"jobassistant/backend/internal/database"
	"jobassistant/backend/internal/llm"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
	"jobassistant/backend/internal/workflow"
	"jobassistant/backend/migrations"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("private worker stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	dsn, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	cfg, err := config.LoadFeatures()
	if err != nil {
		return err
	}
	model, err := llm.New(llm.Options{BaseURL: cfg.ModelBaseURL, Model: cfg.ModelName, APIKey: cfg.ModelAPIKey, AllowHTTP: cfg.AllowHTTP})
	if err != nil {
		return err
	}
	defer model.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = migrations.Check(ctx, pool); err != nil {
		return err
	}
	vault, err := profile.OpenVault(cfg.PrivateDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := vault.Close(); err != nil {
			slog.Warn("private storage close failed")
		}
	}()
	worker := workflow.New(task.NewStore(pool), profile.NewStore(pool, vault), matching.NewStore(pool), model, model)
	slog.Info("private worker started", "model_configured", model.Configured())
	return worker.Run(ctx)
}
