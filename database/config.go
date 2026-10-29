package database

import (
	"time"
)

// Config holds the connection settings and pool limits. Zero fields get the
// defaults below, except SlowQueryThreshold, where 0 turns slow query logs off.
type Config struct {
	// Driver is the name the driver registered with database/sql, such as
	// "pgx" for github.com/jackc/pgx/v5/stdlib or "mysql" for
	// github.com/go-sql-driver/mysql. Import the driver in main.
	Driver          string        `mapstructure:"driver"`
	DSN             string        `mapstructure:"dsn"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time"`
	// PingTimeout limits the connection check in Open.
	PingTimeout time.Duration `mapstructure:"ping_timeout"`
	// SlowQueryThreshold logs a warning for queries that take longer.
	SlowQueryThreshold time.Duration `mapstructure:"slow_query_threshold"`
}

const (
	defaultMaxOpenConns    = 25
	defaultConnMaxLifetime = 30 * time.Minute
	defaultConnMaxIdleTime = 5 * time.Minute
	defaultPingTimeout     = 5 * time.Second
)

func (c Config) withDefaults() Config {
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = defaultMaxOpenConns
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = c.MaxOpenConns
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = defaultConnMaxLifetime
	}
	if c.ConnMaxIdleTime == 0 {
		c.ConnMaxIdleTime = defaultConnMaxIdleTime
	}
	if c.PingTimeout == 0 {
		c.PingTimeout = defaultPingTimeout
	}
	return c
}
