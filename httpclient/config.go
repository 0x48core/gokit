package httpclient

import (
	"time"
)

// Config holds the client's timeouts, connection pool and retry policy. Zero
// fields get the defaults below, except MaxRetries, where 0 means no retries.
type Config struct {
	// Timeout limits a whole call, including retries and reading the body.
	Timeout             time.Duration `mapstructure:"timeout"`
	DialTimeout         time.Duration `mapstructure:"dial_timeout"`
	TLSHandshakeTimeout time.Duration `mapstructure:"tls_handshake_timeout"`
	IdleConnTimeout     time.Duration `mapstructure:"idle_conn_timeout"`
	MaxIdleConnsPerHost int           `mapstructure:"max_idle_conns_per_host"`
	MaxConnsPerHost     int           `mapstructure:"max_conns_per_host"`

	MaxRetries   int           `mapstructure:"max_retries"`
	RetryWaitMin time.Duration `mapstructure:"retry_wait_min"`
	RetryWaitMax time.Duration `mapstructure:"retry_wait_max"`
}

const (
	defaultTimeout             = 10 * time.Second
	defaultDialTimeout         = 5 * time.Second
	defaultTLSHandshakeTimeout = 5 * time.Second
	defaultIdleConnTimeout     = 90 * time.Second
	defaultMaxIdleConnsPerHost = 100
	defaultRetryWaitMin        = 100 * time.Millisecond
	defaultRetryWaitMax        = 2 * time.Second
)

func (c Config) withDefaults() Config {
	if c.Timeout == 0 {
		c.Timeout = defaultTimeout
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = defaultDialTimeout
	}
	if c.TLSHandshakeTimeout == 0 {
		c.TLSHandshakeTimeout = defaultTLSHandshakeTimeout
	}
	if c.IdleConnTimeout == 0 {
		c.IdleConnTimeout = defaultIdleConnTimeout
	}
	if c.MaxIdleConnsPerHost == 0 {
		c.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}
	if c.RetryWaitMin == 0 {
		c.RetryWaitMin = defaultRetryWaitMin
	}
	if c.RetryWaitMax == 0 {
		c.RetryWaitMax = defaultRetryWaitMax
	}
	if c.RetryWaitMax < c.RetryWaitMin {
		c.RetryWaitMax = c.RetryWaitMin
	}
	return c
}
