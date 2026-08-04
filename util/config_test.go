package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A missing app.env must not be fatal -- CI checks out a tree without one, and a
// Kubernetes pod supplies its config as environment variables.
func TestLoadConfigFromEnvironmentWithoutFile(t *testing.T) {
	t.Setenv("ENVIRONMENT", "test")
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("DB_SOURCE", "postgresql://root:psql@localhost:5432/bank?sslmode=disable")
	t.Setenv("TOKEN_SYMMETRIC_KEY", "12345678901234567890123456789012")
	t.Setenv("ACCESS_TOKEN_DURATION", "15m")
	t.Setenv("REFRESH_TOKEN_DURATION", "24h")

	config, err := LoadConfig(t.TempDir())
	require.NoError(t, err)

	require.Equal(t, "test", config.Environment)
	require.Equal(t, "postgres", config.DbDriver)
	require.Equal(t, "postgresql://root:psql@localhost:5432/bank?sslmode=disable", config.DbSource)
	require.Equal(t, "12345678901234567890123456789012", config.TokenSymmetricKey)

	// Durations have to survive the string -> time.Duration decode, not silently
	// land as zero.
	require.Equal(t, 15*time.Minute, config.AccessTokenDuration)
	require.Equal(t, 24*time.Hour, config.RefreshTokenDuration)
}
