package util

import (
	"errors"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Environment          string        `mapstructure:"ENVIRONMENT"`
	DbDriver             string        `mapstructure:"DB_DRIVER"`
	DbSource             string        `mapstructure:"DB_SOURCE"`
	MigrationURL         string        `mapstructure:"MIGRATION_URL"`
	HTTPServerAddress    string        `mapstructure:"HTTP_SERVER_ADDRESS"`
	GRPCServerAddress    string        `mapstructure:"GRPC_SERVER_ADDRESS"`
	TokenSymmetricKey    string        `mapstructure:"TOKEN_SYMMETRIC_KEY"`
	AccessTokenDuration  time.Duration `mapstructure:"ACCESS_TOKEN_DURATION"`
	RefreshTokenDuration time.Duration `mapstructure:"REFRESH_TOKEN_DURATION"`
}

// configKeys has to be bound explicitly: viper's AutomaticEnv only affects
// viper.Get, not Unmarshal, so without a BindEnv per key an environment with no
// config file unmarshals to zero values instead of reading the environment.
var configKeys = []string{
	"ENVIRONMENT",
	"DB_DRIVER",
	"DB_SOURCE",
	"MIGRATION_URL",
	"HTTP_SERVER_ADDRESS",
	"GRPC_SERVER_ADDRESS",
	"TOKEN_SYMMETRIC_KEY",
	"ACCESS_TOKEN_DURATION",
	"REFRESH_TOKEN_DURATION",
}

// LoadConfig reads app.env from path when it is present and otherwise falls back
// to the environment. app.env is not in the repo, so CI and a Kubernetes pod with
// its config supplied as environment variables both work without one; a missing
// file is only an error if the environment does not cover it either, which shows
// up as a zero-valued field at the point of use.
func LoadConfig(path string) (config Config, err error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("app")
	viper.SetConfigType("env") // json, toml, yaml, ini

	viper.AutomaticEnv()
	for _, key := range configKeys {
		if err = viper.BindEnv(key); err != nil {
			return
		}
	}

	if err = viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return
		}
		err = nil // no app.env: the environment is expected to supply the values
	}

	err = viper.Unmarshal(&config)
	return
}
