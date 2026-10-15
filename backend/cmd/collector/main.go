package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/config"
	"jobassistant/backend/internal/database"
	"jobassistant/backend/internal/ingest"
	"jobassistant/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: collector all|tencent-campus|huawei-campus|cmb-campus")
	}
	selected := os.Args[1]
	if selected != "all" && selected != "tencent-campus" && selected != "huawei-campus" && selected != "cmb-campus" {
		return errors.New("unknown public source")
	}
	dsn, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		return errors.New("collector database unavailable")
	}
	defer pool.Close()
	if err := migrations.Check(ctx, pool); err != nil {
		return err
	}
	store := catalog.NewStore(pool)
	sources, err := store.SystemSources(ctx)
	if err != nil {
		return errors.New("source configuration unavailable")
	}
	failed := false
	encoder := json.NewEncoder(os.Stdout)
	for _, source := range sources {
		if selected != "all" && source.Code != selected {
			continue
		}
		if !source.Enabled {
			if err := encoder.Encode(ingest.Result{SourceCode: source.Code, Status: "paused"}); err != nil {
				return err
			}
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 16*time.Minute)
		result := ingest.Collect(readCtx, store, ingest.NewReader(), source.Code)
		cancel()
		if err := encoder.Encode(result); err != nil {
			return errors.New("collection result output failed")
		}
		if result.Status != "succeeded" {
			failed = true
		}
		if ctx.Err() != nil {
			return errors.New("collection cancelled")
		}
	}
	if failed {
		return errors.New("one or more public sources failed; previous jobs were retained")
	}
	return nil
}
