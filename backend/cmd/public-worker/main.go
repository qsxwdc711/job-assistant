// public-worker consumes only explicitly selected public-source tasks.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"jobassistant/backend/internal/automation"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/config"
	"jobassistant/backend/internal/database"
	"jobassistant/backend/internal/ingest"
	"jobassistant/backend/internal/task"
	"jobassistant/backend/migrations"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("host public collector failed")
		os.Exit(1)
	}
}

func run() error {
	sourcesFlag := flag.String("sources", "", "required comma-separated public source codes")
	stopFile := flag.String("stop-file", "", "local cancellation marker")
	flag.Parse()
	var sources []string
	for _, source := range strings.Split(*sourcesFlag, ",") {
		source = strings.TrimSpace(source)
		if source == "" || !ingest.SupportsSource(source) {
			return errors.New("explicit implemented sources required")
		}
		sources = append(sources, source)
	}
	dsn, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	setupCtx, cancelSetup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelSetup()
	pool, err := database.Open(setupCtx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = migrations.Check(setupCtx, pool); err != nil {
		return err
	}
	cancelSetup()
	store := catalog.NewStore(pool)
	worker := &automation.Worker{Collections: task.NewCollectionStore(pool).WithSources(sources, nil), Now: func() time.Time { return time.Now().UTC() }, Collect: func(ctx context.Context, source string) ingest.Result {
		return ingest.Collect(ctx, store, ingest.NewReader(), source)
	}}
	group, runCtx := errgroup.WithContext(ctx)
	if *stopFile != "" {
		group.Go(func() error {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return nil
				case <-ticker.C:
					if _, err := os.Stat(*stopFile); err == nil {
						stop()
						return nil
					} else if !errors.Is(err, os.ErrNotExist) {
						return errors.New("cannot read cancellation marker")
					}
				}
			}
		})
	}
	slog.Info("host public collector started", "sources", sources)
	group.Go(func() error {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if runCtx.Err() != nil {
				return nil
			}
			if _, err := worker.CollectOne(runCtx); err != nil && runCtx.Err() == nil {
				slog.Warn("public collection iteration failed")
			}
			select {
			case <-runCtx.Done():
				return nil
			case <-ticker.C:
			}
		}
	})
	if err = group.Wait(); err != nil {
		return err
	}
	slog.Info("host public collector stopped cleanly")
	return nil
}
