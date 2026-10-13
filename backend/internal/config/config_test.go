package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadAPI(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_ORIGIN", "http://localhost:8080")
	t.Setenv("CSRF_SECRET", "")
	if _, err := LoadAPI(); err == nil {
		t.Fatal("missing secret accepted")
	}
	t.Setenv("CSRF_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))))
	cfg, err := LoadAPI()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecureCookies {
		t.Fatal("development cookie unexpectedly secure")
	}
	t.Setenv("APP_ENV", "production")
	if _, err := LoadAPI(); err == nil {
		t.Fatal("production HTTP origin accepted")
	}
	t.Setenv("APP_ORIGIN", "https://jobs.example.test")
	cfg, err = LoadAPI()
	if err != nil || !cfg.SecureCookies {
		t.Fatalf("production configuration failed: %v", err)
	}
	t.Setenv("DATABASE_URL", "postgres://account:do-not-echo-secret@")
	if _, err := LoadAPI(); err == nil || strings.Contains(err.Error(), "do-not-echo-secret") {
		t.Fatal("invalid database URL leaked or accepted")
	}
}
