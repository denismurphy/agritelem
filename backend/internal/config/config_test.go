package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"AGRITELEM_DATABASE_URL":        "postgres://x",
		"AGRITELEM_JWT_PUBLIC_KEY_FILE": "/keys/pub.pem",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.PublicURL.String() != "http://localhost:8080" || cfg.MigrateOnStart || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	_, err := Load(env(map[string]string{
		"AGRITELEM_PUBLIC_URL":       "localhost:8080",
		"AGRITELEM_MIGRATE_ON_START": "maybe",
		"AGRITELEM_LOG_LEVEL":        "chatty",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"AGRITELEM_DATABASE_URL", "AGRITELEM_JWT_PUBLIC_KEY_FILE", "AGRITELEM_PUBLIC_URL", "AGRITELEM_MIGRATE_ON_START", "AGRITELEM_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}
