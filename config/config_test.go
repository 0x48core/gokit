package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type RedisConfig struct {
	Addr     []string      `mapstructure:"addr" validate:"min=1"`
	PoolSize int           `mapstructure:"pool_size" default:"10"`
	Timeout  time.Duration `mapstructure:"timeout" default:"1s"`
}

type Base struct {
	Env string `mapstructure:"env" default:"local" validate:"oneof=local staging production"`
}

type AppConfig struct {
	Base     `mapstructure:",squash"`
	Name     string       `mapstructure:"name" validate:"required"`
	Port     int          `mapstructure:"port" default:"8080"`
	Debug    bool         `mapstructure:"debug"`
	LogLevel slog.Level   `mapstructure:"log_level" default:"info"`
	Redis    RedisConfig  `mapstructure:"redis"`
	Kafka    *KafkaConfig `mapstructure:"kafka"`
	Ignored  string       `mapstructure:"-"`
}

type KafkaConfig struct {
	Brokers []string `mapstructure:"brokers" default:"localhost:9092"`
}

type checkedConfig struct {
	Min int `mapstructure:"min"`
	Max int `mapstructure:"max"`
}

func (c checkedConfig) Validate() error {
	if c.Min > c.Max {
		return errors.New("min must not exceed max")
	}
	return nil
}

type TestConfigSuite struct {
	suite.Suite
	dir string
}

func TestTestConfigSuite(t *testing.T) {
	suite.Run(t, new(TestConfigSuite))
}

func (suite *TestConfigSuite) SetupTest() {
	suite.dir = suite.T().TempDir()
}

func (suite *TestConfigSuite) writeFile(name, content string) string {
	path := filepath.Join(suite.dir, name)
	suite.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return path
}

func (suite *TestConfigSuite) Test_Defaults() {
	// Arrange
	suite.T().Setenv("NAME", "orders")
	suite.T().Setenv("REDIS_ADDR", "localhost:6379")

	// Action
	var cfg AppConfig
	err := Load(&cfg)

	// Assert
	suite.Require().NoError(err)
	suite.Equal("local", cfg.Env)
	suite.Equal(8080, cfg.Port)
	suite.Equal(slog.LevelInfo, cfg.LogLevel)
	suite.Equal(10, cfg.Redis.PoolSize)
	suite.Equal(time.Second, cfg.Redis.Timeout)
	suite.Require().NotNil(cfg.Kafka)
	suite.Equal([]string{"localhost:9092"}, cfg.Kafka.Brokers)
}

func (suite *TestConfigSuite) Test_File() {
	// Arrange
	path := suite.writeFile("config.yaml", `
env: production
name: orders
port: 9090
log_level: debug
redis:
  addr: [a:6379, b:6379]
  timeout: 2s
`)

	// Action
	var cfg AppConfig
	err := Load(&cfg, WithFile(path))

	// Assert
	suite.Require().NoError(err)
	suite.Equal("production", cfg.Env)
	suite.Equal("orders", cfg.Name)
	suite.Equal(9090, cfg.Port)
	suite.Equal(slog.LevelDebug, cfg.LogLevel)
	suite.Equal([]string{"a:6379", "b:6379"}, cfg.Redis.Addr)
	suite.Equal(2*time.Second, cfg.Redis.Timeout)
	suite.Equal(10, cfg.Redis.PoolSize)
}

func (suite *TestConfigSuite) Test_FilesMergeInOrder() {
	// Arrange
	base := suite.writeFile("base.yaml", "name: orders\nport: 9090\nredis:\n  addr: [a:6379]\n  pool_size: 5\n")
	override := suite.writeFile("override.json", `{"port": 9191, "redis": {"pool_size": 20}}`)

	// Action
	var cfg AppConfig
	err := Load(&cfg, WithFile(base), WithFile(override))

	// Assert
	suite.Require().NoError(err)
	suite.Equal("orders", cfg.Name)
	suite.Equal(9191, cfg.Port)
	suite.Equal([]string{"a:6379"}, cfg.Redis.Addr)
	suite.Equal(20, cfg.Redis.PoolSize)
}

func (suite *TestConfigSuite) Test_EnvOverridesFile() {
	// Arrange
	path := suite.writeFile("config.yaml", "name: orders\nport: 9090\nredis:\n  addr: [a:6379]\n")
	suite.T().Setenv("APP_PORT", "7070")
	suite.T().Setenv("APP_DEBUG", "true")
	suite.T().Setenv("APP_REDIS_ADDR", "x:6379,y:6379")
	suite.T().Setenv("APP_REDIS_TIMEOUT", "500ms")
	suite.T().Setenv("APP_KAFKA_BROKERS", "k1:9092,k2:9092")
	suite.T().Setenv("PORT", "1") // ignored: no prefix

	// Action
	var cfg AppConfig
	err := Load(&cfg, WithFile(path), WithEnvPrefix("app"))

	// Assert
	suite.Require().NoError(err)
	suite.Equal(7070, cfg.Port)
	suite.True(cfg.Debug)
	suite.Equal([]string{"x:6379", "y:6379"}, cfg.Redis.Addr)
	suite.Equal(500*time.Millisecond, cfg.Redis.Timeout)
	suite.Equal([]string{"k1:9092", "k2:9092"}, cfg.Kafka.Brokers)
}

func (suite *TestConfigSuite) Test_OptionalFileMissing() {
	// Arrange
	path := suite.writeFile("config.yaml", "name: orders\nredis:\n  addr: [a:6379]\n")

	// Action
	var cfg AppConfig
	err := Load(&cfg, WithFile(path), WithOptionalFile(filepath.Join(suite.dir, "local.yaml")))

	// Assert
	suite.Require().NoError(err)
	suite.Equal("orders", cfg.Name)
}

func (suite *TestConfigSuite) Test_RequiredFileMissing() {
	// Action
	var cfg AppConfig
	err := Load(&cfg, WithFile(filepath.Join(suite.dir, "missing.yaml")))

	// Assert
	suite.ErrorContains(err, "missing.yaml")
}

func (suite *TestConfigSuite) Test_Strict() {
	// Arrange
	path := suite.writeFile("config.yaml", "name: orders\nredis:\n  addr: [a:6379]\n  pool_sise: 5\n")

	// Action
	var lax, strict AppConfig
	laxErr := Load(&lax, WithFile(path))
	strictErr := Load(&strict, WithFile(path), WithStrict())

	// Assert
	suite.NoError(laxErr)
	suite.ErrorContains(strictErr, "pool_sise")
}

func (suite *TestConfigSuite) Test_ValidationErrorsUseKeys() {
	// Arrange
	suite.T().Setenv("ENV", "dev")

	// Action
	var cfg AppConfig
	err := Load(&cfg)

	// Assert
	suite.Require().Error(err)
	suite.Contains(err.Error(), "env: failed oneof=local staging production validation")
	suite.Contains(err.Error(), "name: failed required validation")
	suite.Contains(err.Error(), "redis.addr: failed min=1 validation")
}

func (suite *TestConfigSuite) Test_Validator() {
	// Arrange
	suite.T().Setenv("MIN", "5")
	suite.T().Setenv("MAX", "1")

	// Action
	var cfg checkedConfig
	err := Load(&cfg)

	// Assert
	suite.ErrorContains(err, "min must not exceed max")
}

func (suite *TestConfigSuite) Test_IgnoredField() {
	// Arrange
	suite.T().Setenv("NAME", "orders")
	suite.T().Setenv("REDIS_ADDR", "a:6379")
	suite.T().Setenv("IGNORED", "x")

	// Action
	cfg := AppConfig{Ignored: "kept"}
	err := Load(&cfg)

	// Assert
	suite.Require().NoError(err)
	suite.Equal("kept", cfg.Ignored)
}

func (suite *TestConfigSuite) Test_InvalidOut() {
	var cfg AppConfig
	var nilCfg *AppConfig
	n := 1

	suite.Error(Load(cfg))
	suite.Error(Load(nilCfg))
	suite.Error(Load(&n))
}

func (suite *TestConfigSuite) Test_DecodeError() {
	// Arrange
	suite.T().Setenv("PORT", "not-a-number")

	// Action
	var cfg AppConfig
	err := Load(&cfg)

	// Assert
	suite.ErrorContains(err, "config: decode")
}

func (suite *TestConfigSuite) Test_MustLoad() {
	var cfg AppConfig
	suite.Panics(func() { MustLoad(&cfg) })
}

func (suite *TestConfigSuite) Test_EnvName() {
	suite.Equal("REDIS_POOL_SIZE", envName("", "redis.pool_size"))
	suite.Equal("APP_REDIS_POOL_SIZE", envName("app", "redis.pool_size"))
}
