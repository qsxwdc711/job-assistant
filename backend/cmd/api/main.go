package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/config"
	"jobassistant/backend/internal/database"
	"jobassistant/backend/internal/httpapi"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/llm"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
	"jobassistant/backend/migrations"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadAPI()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	features, err := config.LoadFeatures()
	if err != nil {
		return err
	}
	model, err := llm.New(llm.Options{BaseURL: features.ModelBaseURL, Model: features.ModelName, APIKey: features.ModelAPIKey, AllowHTTP: features.AllowHTTP})
	if err != nil {
		return err
	}
	defer model.Close()
	vault, err := profile.OpenVault(features.PrivateDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := vault.Close(); err != nil {
			slog.Warn("private storage close failed")
		}
	}()
	if err := migrations.Check(ctx, pool); err != nil {
		return err
	}
	service, err := identity.NewService(identity.NewStore(pool), identity.Options{})
	if err != nil {
		return err
	}
	handler, err := httpapi.NewHandler(service, httpapi.Config{CSRFKey: cfg.CSRFKey, SecureCookies: cfg.SecureCookies, Origin: cfg.Origin}, pool.Ping,
		httpapi.WithRecruitment(catalog.NewStore(pool), application.NewStore(pool)), httpapi.WithFrontend(os.Getenv("WEB_ROOT")), httpapi.WithResumes(profile.NewStore(pool, vault), matching.NewStore(pool), task.NewStore(pool), model.Configured(), model.Version()))
	if err != nil {
		return err
	}
	defer func() {
		if err := handler.Close(); err != nil {
			slog.Warn("frontend directory close failed")
		}
	}()
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	slog.Info("API started", "address", cfg.HTTPAddr)
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			if closeErr := server.Close(); closeErr != nil {
				slog.Warn("server close failed")
			}
			return err
		}
		err := <-result
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		slog.Info("API stopped cleanly")
		return nil
	}
}
