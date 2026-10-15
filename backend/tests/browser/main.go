package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/config"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "isolated browser fixture initialization failed:", err)
		os.Exit(1)
	}
}
func run() error {
	dsn, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		return fmt.Errorf("dedicated test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrations.Apply(ctx, pool); err != nil {
		return err
	}
	service, err := identity.NewService(identity.NewStore(pool), identity.Options{PasswordCost: 4})
	if err != nil {
		return err
	}
	admin, err := service.Bootstrap(ctx, "web-admin", os.Getenv("S2_BROWSER_PASSWORD"))
	if err != nil {
		return err
	}
	for _, name := range []string{"web-user-a", "web-user-b"} {
		if _, err := service.CreateUser(ctx, admin, identity.CreateUserInput{Username: name, Password: os.Getenv("S2_BROWSER_PASSWORD"), Role: identity.RoleStandard}, "browser-fixture"); err != nil {
			return err
		}
	}
	filename := os.Getenv("S2_BROWSER_JOB_FILE")
	if filename == "" {
		filename = "tests/browser/testdata/tencent-job.json"
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var envelope struct {
		Items []catalog.Document `json:"items"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	store := catalog.NewStore(pool)
	for _, doc := range envelope.Items {
		now := time.Now().UTC()
		run, err := store.BeginRun(ctx, doc.SourceCode, now)
		if err != nil {
			return err
		}
		if err := store.Publish(ctx, run, []catalog.Document{doc}, 1, nil, time.Now().UTC()); err != nil {
			return err
		}
	}
	fmt.Println("Isolated browser accounts and real public job snapshots initialized.")
	return nil
}
