package config

import (
	"errors"
	"os"
	"path/filepath"
)

// Features configures private storage and the optional external model in API and worker processes.
type Features struct {
	PrivateDir, ModelBaseURL, ModelName, ModelAPIKey string
	AllowHTTP                                        bool
}

// LoadFeatures defaults to local-only processing when MODEL_API_KEY is absent.
func LoadFeatures() (Features, error) {
	cfg := Features{PrivateDir: os.Getenv("PRIVATE_STORAGE_DIR"), ModelBaseURL: os.Getenv("MODEL_BASE_URL"), ModelName: os.Getenv("MODEL_NAME"), ModelAPIKey: os.Getenv("MODEL_API_KEY")}
	if cfg.PrivateDir == "" {
		cfg.PrivateDir = "/app/private"
	}
	if !filepath.IsAbs(cfg.PrivateDir) {
		return cfg, errors.New("PRIVATE_STORAGE_DIR must be absolute")
	}
	if os.Getenv("MODEL_ALLOW_HTTP") == "true" {
		if os.Getenv("APP_ENV") != "development" {
			return cfg, errors.New("MODEL_ALLOW_HTTP requires development environment")
		}
		cfg.AllowHTTP = true
	}
	return cfg, nil
}
