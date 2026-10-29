# Config

Loads configuration into a struct from defaults, files and environment variables, built on
[viper](https://github.com/spf13/viper) and [validator](https://github.com/go-playground/validator).

- Defaults from `default` struct tags
- YAML, JSON or TOML files, merged in order
- Environment variables for every field, derived from its key: `redis.pool_size` → `REDIS_POOL_SIZE`
- Validation with `validate` tags, with errors that name the config key
- Fields keyed by `mapstructure` tags, so the `Config` structs of the other gokit packages load as-is

Each source overrides the previous one: defaults, then files, then environment variables.

## Installation

```sh
go get github.com/0x48core/gokit/config
```

Requires Go 1.25+.

## Usage

```go
type Config struct {
	Env      string        `mapstructure:"env" default:"local" validate:"oneof=local staging production"`
	Port     int           `mapstructure:"port" default:"8080"`
	LogLevel slog.Level    `mapstructure:"log_level" default:"info"`
	Timeout  time.Duration `mapstructure:"timeout" default:"5s"`
	Redis    redis.Config  `mapstructure:"redis"`
	Kafka    struct {
		Brokers []string `mapstructure:"brokers" validate:"min=1"`
	} `mapstructure:"kafka"`
}

var cfg Config
err := config.Load(&cfg,
	config.WithFile("config.yaml"),
	config.WithOptionalFile("config.local.yaml"), // skipped when missing
	config.WithEnvPrefix("ORDERS"),
)
```

```yaml
# config.yaml
env: production
redis:
  addr: [redis-1:6379, redis-2:6379]
  pool_size: 20
kafka:
  brokers: [kafka-1:9092]
```

```sh
ORDERS_PORT=9090
ORDERS_REDIS_ADDR=localhost:6379,localhost:6380   # slices are comma-separated
ORDERS_KAFKA_BROKERS=localhost:9092
```

In `main`, `config.MustLoad` panics instead of returning the error.

### Keys

A field's key is its `mapstructure` tag, or its lowercased name when it has none. Nested structs
add a level, `mapstructure:",squash"` embeds a struct's fields in its parent, and `mapstructure:"-"`
skips a field. Use snake_case tags: a field `PoolSize` without a tag is read from `POOLSIZE`.

Strings are converted to the field type, so `default` tags and environment variables work for
numbers, booleans, `time.Duration`, comma-separated slices and any type that implements
`encoding.TextUnmarshaler`, such as `slog.Level`.

### Validation

`validate` tags use the [validator](https://pkg.go.dev/github.com/go-playground/validator/v10)
syntax. Failures are joined into one error:

```text
config: env: failed oneof=local staging production validation
kafka.brokers: failed min=1 validation
```

For checks across fields, implement `Validator`. Its `Validate` method is called after the tags pass:

```go
func (c Config) Validate() error {
	if c.Env == "production" && c.LogLevel < slog.LevelInfo {
		return errors.New("debug logging is not allowed in production")
	}
	return nil
}
```

### Strict mode

`config.WithStrict()` fails when a file has a key that matches no field, which catches typos such
as `pool_sise`.

## API

| Name | Description |
| --- | --- |
| `Load(out, opts...)` | Fills the struct `out` points to and validates it. |
| `MustLoad(out, opts...)` | Like `Load`, but panics on error. |
| `WithFile(path)` | Reads a file; fails if it is missing. Later files override earlier ones. |
| `WithOptionalFile(path)` | Reads a file if it exists. |
| `WithEnvPrefix(prefix)` | Reads `PREFIX_KEY` instead of `KEY`. |
| `WithStrict()` | Fails on unknown keys in files. |
| `Validator` | Interface for checks that `validate` tags cannot express. |

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
