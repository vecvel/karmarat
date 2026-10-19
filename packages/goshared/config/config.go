// Package config loads service configuration from the environment.
//
// Every service reads the same base settings (datastores, observability, the
// platform hostname) and layers its own on top, so a service never invents its
// own convention for something the platform already defines.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Base is the configuration shared by every service.
type Base struct {
	ServiceName string
	Environment string
	Hostname    string
	LogLevel    string

	PostgresURL string
	RedisURL    string
	NATSURL     string

	OTLPEndpoint string
}

// LoadBase reads the shared configuration for a service.
func LoadBase(serviceName string) (Base, error) {
	LoadDotEnv()

	cfg := Base{
		ServiceName:  serviceName,
		Environment:  String("PLATFORM_ENV", "development"),
		Hostname:     String("PLATFORM_HOSTNAME", "karmarat.local"),
		LogLevel:     String("LOG_LEVEL", "info"),
		NATSURL:      String("NATS_URL", "nats://localhost:4222"),
		OTLPEndpoint: String("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
	}

	// Each service owns its own database; the name defaults to the service name.
	dbName := String("POSTGRES_DB", serviceName)
	cfg.PostgresURL = String("DATABASE_URL", postgresURL(dbName))
	cfg.RedisURL = String("REDIS_URL", redisURL())

	if cfg.Environment == "production" {
		if err := requireProduction(cfg); err != nil {
			return Base{}, err
		}
	}

	return cfg, nil
}

func requireProduction(cfg Base) error {
	var missing []string
	if os.Getenv("DATABASE_URL") == "" && os.Getenv("POSTGRES_PASSWORD") == "" {
		missing = append(missing, "DATABASE_URL or POSTGRES_PASSWORD")
	}
	if os.Getenv("REDIS_URL") == "" && os.Getenv("REDIS_PASSWORD") == "" {
		missing = append(missing, "REDIS_URL or REDIS_PASSWORD")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: missing required production settings: %s", strings.Join(missing, ", "))
	}
	return nil
}

func postgresURL(database string) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		String("POSTGRES_USER", "karmarat"),
		String("POSTGRES_PASSWORD", "karmarat"),
		String("POSTGRES_HOST", "localhost"),
		String("POSTGRES_PORT", "5432"),
		database,
		String("POSTGRES_SSLMODE", "disable"),
	)
}

func redisURL() string {
	password := String("REDIS_PASSWORD", "")
	auth := ""
	if password != "" {
		auth = ":" + password + "@"
	}
	return fmt.Sprintf("redis://%s%s:%s/%s",
		auth,
		String("REDIS_HOST", "localhost"),
		String("REDIS_PORT", "6379"),
		String("REDIS_DB", "0"),
	)
}

// String reads an environment variable, falling back to a default.
func String(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// MustString reads a required environment variable.
func MustString(key string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("config: required environment variable %q is not set", key)
}

// Int reads an integer environment variable, falling back to a default.
func Int(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

// Bool reads a boolean environment variable, falling back to a default.
func Bool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

// Duration reads a duration environment variable (e.g. "15m"), falling back to a default.
func Duration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return parsed
}
