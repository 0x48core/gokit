package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Validator is implemented by configs that need checks beyond `validate` tags.
// Validate is called after the tags pass.
type Validator interface {
	Validate() error
}

type file struct {
	path     string
	optional bool
}

type options struct {
	files     []file
	envPrefix string
	strict    bool
}

type Option func(*options)

// WithFile reads a YAML, JSON or TOML file, picked by its extension. Files are
// merged in the order given, so later files override earlier ones. Load fails
// if the file does not exist.
func WithFile(path string) Option {
	return func(o *options) {
		o.files = append(o.files, file{path: path})
	}
}

// WithOptionalFile is like WithFile, but a missing file is skipped.
func WithOptionalFile(path string) Option {
	return func(o *options) {
		o.files = append(o.files, file{path: path, optional: true})
	}
}

// WithEnvPrefix prepends prefix and an underscore to every environment variable
// name, so the key redis.pool_size is read from APP_REDIS_POOL_SIZE.
func WithEnvPrefix(prefix string) Option {
	return func(o *options) {
		o.envPrefix = prefix
	}
}

// WithStrict makes Load fail when a file has keys that match no field.
func WithStrict() Option {
	return func(o *options) {
		o.strict = true
	}
}

// Load fills out, a pointer to a struct, from `default` tags, then the files,
// then environment variables, each overriding the previous one. It then checks
// `validate` tags and calls Validate if out implements Validator.
func Load(out any, opts ...Option) error {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: out must be a non-nil pointer to a struct, got %T", out)
	}

	v := viper.New()
	for _, f := range collectFields(rv.Elem().Type(), "") {
		if f.hasDefault {
			v.SetDefault(f.key, f.def)
		}
		if err := v.BindEnv(f.key, envName(o.envPrefix, f.key)); err != nil {
			return fmt.Errorf("config: bind env for %s: %w", f.key, err)
		}
	}

	for _, f := range o.files {
		if f.optional {
			if _, err := os.Stat(f.path); errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
		v.SetConfigFile(f.path)
		if err := v.MergeInConfig(); err != nil {
			return fmt.Errorf("config: read %s: %w", f.path, err)
		}
	}

	hook := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		mapstructure.TextUnmarshallerHookFunc(),
	))
	unmarshal := v.Unmarshal
	if o.strict {
		unmarshal = v.UnmarshalExact
	}
	if err := unmarshal(out, hook); err != nil {
		return fmt.Errorf("config: decode: %w", err)
	}

	if err := validate(out); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// MustLoad is like Load but panics on error. It is meant for main.
func MustLoad(out any, opts ...Option) {
	if err := Load(out, opts...); err != nil {
		panic(err)
	}
}
