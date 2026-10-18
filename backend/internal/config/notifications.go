package config

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
)

// Notifications configures scheduling, encryption and the default-off outbound switch.
type Notifications struct {
	Key             []byte
	SendEnabled     bool
	Origin          string
	ScheduleEnabled bool
}

// LoadNotifications rejects malformed keys and ambiguous boolean settings.
func LoadNotifications() (Notifications, error) {
	cfg := Notifications{Origin: strings.TrimRight(os.Getenv("APP_ORIGIN"), "/"), ScheduleEnabled: true}
	if cfg.Origin == "" && os.Getenv("APP_ENV") == "development" {
		cfg.Origin = "http://localhost:8080"
	}
	if raw := os.Getenv("NOTIFICATION_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return cfg, errors.New("NOTIFICATION_KEY must be base64 encoding of 32 random bytes")
		}
		cfg.Key = key
	}
	switch os.Getenv("NOTIFICATIONS_SEND_ENABLED") {
	case "", "false":
	case "true":
		cfg.SendEnabled = true
	default:
		return cfg, errors.New("NOTIFICATIONS_SEND_ENABLED must be true or false")
	}
	if cfg.SendEnabled && len(cfg.Key) == 0 {
		return cfg, errors.New("real notifications require NOTIFICATION_KEY")
	}
	switch os.Getenv("AUTOMATION_ENABLED") {
	case "", "true":
	case "false":
		cfg.ScheduleEnabled = false
	default:
		return cfg, errors.New("AUTOMATION_ENABLED must be true or false")
	}
	return cfg, nil
}
