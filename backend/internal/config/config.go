// Package config reads the API server's configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// Config is the API server configuration.
type Config struct {
	DatabaseURL      string
	ListenAddr       string
	PublicURL        *url.URL
	JWTPublicKeyFile string
	JWTIssuer        string
	JWTAudience      string
	MigrateOnStart   bool
	LogLevel         slog.Level
}

// Load reads configuration through getenv (os.Getenv outside tests). It
// reports every problem at once rather than the first.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	required := func(key string) string {
		v := get(key, "")
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
		}
		return v
	}

	cfg := Config{
		DatabaseURL:      required("AGRITELEM_DATABASE_URL"),
		ListenAddr:       get("AGRITELEM_LISTEN_ADDR", ":8080"),
		JWTPublicKeyFile: required("AGRITELEM_JWT_PUBLIC_KEY_FILE"),
		JWTIssuer:        get("AGRITELEM_JWT_ISSUER", "agritelem-dev"),
		JWTAudience:      get("AGRITELEM_JWT_AUDIENCE", "agritelem-api"),
	}

	public := get("AGRITELEM_PUBLIC_URL", "http://localhost:8080")
	u, err := url.Parse(public)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("AGRITELEM_PUBLIC_URL %q must be an absolute http(s) URL", public))
	} else {
		cfg.PublicURL = u
	}

	if v := get("AGRITELEM_MIGRATE_ON_START", "false"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("AGRITELEM_MIGRATE_ON_START %q is not a boolean", v))
		}
		cfg.MigrateOnStart = b
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("AGRITELEM_LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("AGRITELEM_LOG_LEVEL: %w", err))
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}
