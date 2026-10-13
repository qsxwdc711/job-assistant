// Package config validates process configuration without exposing secrets.
package config

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

// API contains validated HTTP process settings.
type API struct {
	DatabaseURL   string
	HTTPAddr      string
	Origin        string
	CSRFKey       []byte
	SecureCookies bool
}

// DatabaseURL loads the required connection URL.
func DatabaseURL() (string, error) {
	raw := os.Getenv("DATABASE_URL")
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("DATABASE_URL must be a PostgreSQL URL")
	}
	return raw, nil
}

// LoadAPI requires an explicit CSRF secret and secure production origin.
func LoadAPI() (API, error) {
	var cfg API
	dsn, err := DatabaseURL()
	if err != nil {
		return cfg, err
	}
	cfg.DatabaseURL = dsn
	cfg.HTTPAddr = os.Getenv("HTTP_ADDR")
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return cfg, errors.New("HTTP_ADDR must contain host and port")
	}
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "production"
	}
	if env != "production" && env != "development" {
		return cfg, errors.New("APP_ENV must be production or development")
	}
	cfg.SecureCookies = env == "production"
	cfg.Origin = strings.TrimRight(os.Getenv("APP_ORIGIN"), "/")
	if cfg.Origin == "" && !cfg.SecureCookies {
		cfg.Origin = "http://localhost:8080"
	}
	origin, err := url.Parse(cfg.Origin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" ||
		(origin.Scheme != "https" && origin.Scheme != "http") || (cfg.SecureCookies && origin.Scheme != "https") {
		return cfg, errors.New("APP_ORIGIN must be an absolute origin; production requires HTTPS")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("CSRF_SECRET"))
	if err != nil || len(key) != 32 {
		return cfg, errors.New("CSRF_SECRET must be base64 encoding of 32 random bytes")
	}
	cfg.CSRFKey = key
	return cfg, nil
}
