package config

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestNotificationDeliveryDefaultsAndExplicitOptIn(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_ORIGIN", "http://localhost:8080/")
	t.Setenv("NOTIFICATION_KEY", "")
	t.Setenv("NOTIFICATIONS_SEND_ENABLED", "")
	t.Setenv("AUTOMATION_ENABLED", "")
	cfg, err := LoadNotifications()
	if err != nil || cfg.SendEnabled || !cfg.ScheduleEnabled || cfg.Origin != "http://localhost:8080" {
		t.Fatalf("unsafe notification defaults: %#v %v", cfg, err)
	}
	t.Setenv("NOTIFICATIONS_SEND_ENABLED", "true")
	if _, err = LoadNotifications(); err == nil {
		t.Fatal("real sending accepted without encryption")
	}
	t.Setenv("NOTIFICATION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	t.Setenv("AUTOMATION_ENABLED", "false")
	cfg, err = LoadNotifications()
	if err != nil || !cfg.SendEnabled || cfg.ScheduleEnabled || len(cfg.Key) != 32 {
		t.Fatal("explicit notification settings rejected")
	}
	for _, key := range []string{"NOTIFICATIONS_SEND_ENABLED", "AUTOMATION_ENABLED"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "yes")
			if _, err := LoadNotifications(); err == nil {
				t.Fatal("ambiguous boolean enabled notifications")
			}
		})
	}
}
