package redis

import (
	"time"
)

type Config struct {
	Addr            []string      `mapstructure:"addr"`
	Username        string        `mapstructure:"username"`
	Password        string        `mapstructure:"password"`
	PoolSize        int           `mapstructure:"pool_size"`
	MinIdleConn     int           `mapstructure:"min_idle_conn"`
	MaxRetries      int           `mapstructure:"max_retries"`
	PoolTimeout     time.Duration `mapstructure:"pool_timeout"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	EnableReadSlave bool          `mapstructure:"enable_read_slave"`
}
