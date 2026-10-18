package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"
	"jobassistant/backend/internal/automation"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/config"
	"jobassistant/backend/internal/database"
	"jobassistant/backend/internal/llm"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/notification"
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
	notificationConfig, err := config.LoadNotifications()
	if err != nil {
		return err
	}
	notifications, err := notification.NewStore(pool, notification.Options{Key: notificationConfig.Key, Origin: notificationConfig.Origin, ModelVersion: model.Version(), SendEnabled: notificationConfig.SendEnabled})
	if err != nil {
		return err
	}
	var excluded []string
	for _, source := range strings.Split(os.Getenv("COLLECTOR_EXCLUDED_SOURCES"), ",") {
		if source = strings.TrimSpace(source); source != "" {
			excluded = append(excluded, source)
		}
	}
	publicWorker := automation.New(task.NewCollectionStore(pool).WithSources(nil, excluded), notifications, catalog.NewStore(pool), notificationConfig.ScheduleEnabled)
	slog.Info("private worker started", "model_configured", model.Configured())
	group, workerCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return worker.Run(workerCtx) })
	group.Go(func() error { return publicWorker.Run(workerCtx) })
	return group.Wait()
}
